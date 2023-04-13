
# Build info
 
GIT_COMMIT              ?= $(shell git rev-parse HEAD)
VERSION_FLAGS           ?= -ldflags "GitCommit=$(GIT_COMMIT)"
GO_BUILD_TAGS           ?= upstream
VERSION                 ?= $(shell git describe --tags --always --dirty)
# Image URL to use all building/pushing controller image targets
CONTROLLER_IMG          ?= quay.io/operator-framework/catalogd-controller
# Image URL to use all building/pushing apiserver image targets
SERVER_IMG              ?= quay.io/operator-framework/catalogd-server
# Tag to use when building/pushing images
IMG_TAG                 ?= devel
## Location to build controller/apiserver binaries in
LOCALBIN                ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)


# Dependencies
CERT_MGR_VERSION        ?= v1.11.0
ENVTEST_SERVER_VERSION = $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1.x/')

# Cluster configuration
CLUSTER_NAME            ?= catalogd
CATALOGD_NAMESPACE      ?= catalogd-system
CLUSTER_TOOL            ?= kind

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

clean: ## Remove binaries and test artifacts
	rm -rf bin

.PHONY: generate
generate: controller-gen ## Generate code and manifests.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test-unit: generate fmt vet setup-envtest ## Run tests.
	eval $$($(SETUP_ENVTEST) use -p env $(ENVTEST_SERVER_VERSION)) && go test ./... -coverprofile cover.out

.PHONY: tidy
tidy: ## Update dependencies
	go mod tidy

.PHONY: verify
verify: tidy fmt generate ## Verify the current code generation and lint
	git diff --exit-code

##@ Build

.PHONY: build-controller
build-controller: generate fmt vet ## Build manager binary.
	CGO_ENABLED=0 GOOS=linux go build -tags $(GO_BUILD_TAGS) $(VERSION_FLAGS) -o bin/manager cmd/manager/main.go

.PHONY: build-server
build-server: fmt vet ## Build api-server binary.
	CGO_ENABLED=0 GOOS=linux go build -tags $(GO_BUILD_TAGS) $(VERSION_FLAGS) -o bin/apiserver cmd/apiserver/main.go

.PHONY: run
run: generate fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: docker-build-controller
docker-build-controller: build-controller test ## Build docker image with the controller manager.
	docker build -f controller.Dockerfile -t ${CONTROLLER_IMG}:${IMG_TAG} bin/

.PHONY: docker-push-controller
docker-push-controller: ## Push docker image with the controller manager.
	docker push ${CONTROLLER_IMG}

.PHONY: docker-build-server
docker-build-server: build-server test ## Build docker image with the apiserver.
	docker build -f apiserver.Dockerfile -t ${SERVER_IMG}:${IMG_TAG} bin/

.PHONY: docker-push-server
docker-push-server: ## Push docker image with the apiserver.
	docker push ${SERVER_IMG}

##@ Deploy

.PHONY: cluster
cluster: $(CLUSTER_TOOL)-cluster ## Standup a local cluster using the tooling specified in the CLUSTER_TOOL variable. Currently supported tools are kind and k3d

.PHONY: cluster-cleanup
cluster-cleanup: $(CLUSTER_TOOL)-cluster-cleanup ## Delete the local cluster using the tooling specified in the CLUSTER_TOOL variable. Currently supported tools are kind and k3d

.PHONY: cluster-load
cluster-load: $(CLUSTER_TOOL)-load ## Load the built images onto the local cluster using the tooling specified in the CLUSTER_TOOL variable. Currently supported tools are kind and k3d

.PHONY: kind-cluster
kind-cluster: kind kind-cluster-cleanup ## Standup a kind cluster
	$(KIND) create cluster --name ${CLUSTER_NAME} 
	$(KIND) export kubeconfig --name ${CLUSTER_NAME}

.PHONY: kind-cluster-cleanup
kind-cluster-cleanup: kind ## Delete the kind cluster
	$(KIND) delete cluster --name ${CLUSTER_NAME}

.PHONY: kind-load
kind-load: kind ## Load the built images onto the kind cluster 
	$(KIND) export kubeconfig --name ${CLUSTER_NAME}
	$(KIND) load docker-image $(CONTROLLER_IMG):${IMG_TAG} --name $(CLUSTER_NAME)
	$(KIND) load docker-image $(SERVER_IMG):${IMG_TAG} --name $(CLUSTER_NAME)

.PHONY: k3d-cluster
k3d-cluster: k3d k3d-cluster-cleanup ## Standup a k3d cluster
	$(K3D) cluster create ${CLUSTER_NAME} 
	$(K3D) kubeconfig merge -d ${CLUSTER_NAME}

.PHONY: k3d-cluster-cleanup
k3d-cluster-cleanup: k3d ## Delete the k3d cluster
	$(K3D) cluster delete ${CLUSTER_NAME}

.PHONY: k3d-load
k3d-load: k3d ## Load the built images onto the k3d cluster 
	$(K3D) kubeconfig merge -d ${CLUSTER_NAME}
	$(K3D) image import $(CONTROLLER_IMG):${IMG_TAG} --cluster $(CLUSTER_NAME)
	$(K3D) image import $(SERVER_IMG):${IMG_TAG} --cluster $(CLUSTER_NAME)

.PHONY: install 
install: docker-build-server docker-build-controller cluster-load cert-manager deploy wait ## Install local catalogd
	
.PHONY: deploy
deploy: kustomize ## Deploy CatalogSource controller and ApiServer to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${CONTROLLER_IMG}:${IMG_TAG}
	cd config/apiserver && $(KUSTOMIZE) edit set image apiserver=${SERVER_IMG}:${IMG_TAG}
ifeq "$(CLUSTER_TOOL)" "k3d"
	sed -i -E 's/standard/local-path/' config/etcd/etcd.yaml
endif
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy CatalogSource controller and ApiServer from the K8s cluster specified in ~/.kube/config. 
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=true -f -	

.PHONY: uninstall 
uninstall: undeploy ## Uninstall local catalogd
	kubectl wait --for=delete namespace/$(CATALOGD_NAMESPACE) --timeout=60s

.PHONY: cert-manager
cert-manager: ## Deploy cert-manager on the cluster
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/$(CERT_MGR_VERSION)/cert-manager.yaml
	kubectl wait --for=condition=Available --namespace=cert-manager deployment/cert-manager-webhook --timeout=60s

wait:
	kubectl wait --for=condition=Available --namespace=$(CATALOGD_NAMESPACE) deployment/catalogd-apiserver --timeout=60s
	kubectl wait --for=condition=Available --namespace=$(CATALOGD_NAMESPACE) deployment/catalogd-controller-manager --timeout=60s
	kubectl rollout status --watch --namespace=$(CATALOGD_NAMESPACE) statefulset/catalogd-etcd --timeout=60s

##@ Release

export ENABLE_RELEASE_PIPELINE ?= false
export GORELEASER_ARGS ?= --snapshot --clean
export CONTROLLER_IMAGE_REPO ?= $(CONTROLLER_IMG)
export APISERVER_IMAGE_REPO ?= $(SERVER_IMG)
export IMAGE_TAG ?= $(IMG_TAG)
release: goreleaser ## Runs goreleaser for catalogd. By default, this will run only as a snapshot and will not publish any artifacts unless it is run with different arguments. To override the arguments, run with "GORELEASER_ARGS=...". When run as a github action from a tag, this target will publish a full release.
	$(GORELEASER) $(GORELEASER_ARGS)

quickstart: kustomize generate ## Generate the installation release manifests and scripts
	kubectl kustomize config/default | sed "s/:devel/:$(VERSION)/g" > catalogd.yaml
	
################
# Hack / Tools #
################
TOOLS_DIR := $(shell pwd)/hack/tools
TOOLS_BIN_DIR := $(TOOLS_DIR)/bin
$(TOOLS_BIN_DIR):
	mkdir -p $(TOOLS_BIN_DIR)


KUSTOMIZE_VERSION ?= v5.0.1
KIND_VERSION ?= v0.15.0
CONTROLLER_TOOLS_VERSION ?= v0.10.0
GORELEASER_VERSION ?= v1.16.2
ENVTEST_VERSION ?= latest

##@ hack/tools:

.PHONY: controller-gen goreleaser kind setup-envtest kustomize

CONTROLLER_GEN := $(abspath $(TOOLS_BIN_DIR)/controller-gen)
SETUP_ENVTEST := $(abspath $(TOOLS_BIN_DIR)/setup-envtest)
GORELEASER := $(abspath $(TOOLS_BIN_DIR)/goreleaser)
KIND := $(abspath $(TOOLS_BIN_DIR)/kind)
K3D := $(abspath $(TOOLS_BIN_DIR)/k3d)
KUSTOMIZE := $(abspath $(TOOLS_BIN_DIR)/kustomize)

kind: $(TOOLS_BIN_DIR) ## Build a local copy of kind
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/kind@$(KIND_VERSION)

controller-gen: $(TOOLS_BIN_DIR) ## Build a local copy of controller-gen
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

goreleaser: $(TOOLS_BIN_DIR) ## Build a local copy of goreleaser
	GOBIN=$(TOOLS_BIN_DIR) go install github.com/goreleaser/goreleaser@$(GORELEASER_VERSION)

setup-envtest: $(TOOLS_BIN_DIR) ## Build a local copy of envtest
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

kustomize: $(TOOLS_BIN_DIR) ## Build a local copy of kustomize
	GOBIN=$(TOOLS_BIN_DIR) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)

k3d: $(TOOLS_BIN_DIR) ## Download a local copy of k3d
	GOBIN=$(TOOLS_BIN_DIR) go install github.com/k3d-io/k3d/v5@v5.4.9

#!/usr/bin/env bash

set -e

# registry.sh will create an in-cluster image registry useful for end-to-end testing
# of catalogd's unpacking process. It does a few things:
# 1. Installs cert-manager for creating a self-signed certificate for the image registry
# 2. Creates all the resources necessary for deploying the image registry in the catalogd-e2e namespace
# 3. Creates a ConfigMap containing the CA cert for the image registry to be used by the catalogd-controller-manager
# 4. Creates a ConfigMap containing the CA cert for the image registry to be used by the kaniko pod
# 5. Creates ConfigMaps containing the test catalog + Dockerfile to be mounted to the kaniko pod
# 6. Waits for kaniko pod to have Phase == Succeeded, indicating the test catalog image has been built + pushed
# to the test image registry
# Usage:
# registry.sh

# Install cert-manager
kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/v1.13.1/cert-manager.yaml"
kubectl wait --for=condition=Available --namespace=cert-manager deployment/cert-manager-webhook --timeout=60s

# create the image registry with all the certs
kubectl apply -f test/tools/imageregistry/imgreg.yaml
kubectl wait -n catalogd-e2e --for=condition=Ready pods/docker-registry-pod --timeout=60s

# get cert value
certval=$(kubectl -n catalogd-e2e get secret/root-secret -o=jsonpath='{.data.ca\.crt}' | base64 -d | sed 's/^/    /')

# create a ConfigMap that contains the CA certs for the image registry
# This one is created for the catalogd-controller-manager
kubectl apply -f - << EOF
apiVersion: v1
kind: ConfigMap
metadata: 
  namespace: catalogd-system
  name: docker-registry.catalogd-e2e.svc
data:
  "ca-certificates.crt": |
${certval}
EOF

# create a ConfigMap that contains the CA certs for the image registry
# this one is created for the kaniko pod
kubectl apply -f - << EOF
apiVersion: v1
kind: ConfigMap
metadata: 
  namespace: catalogd-e2e
  name: regcerts
data:
  "ca-certificates.crt": |
${certval}
EOF

# Load the testdata onto the cluster as a configmap so it can be used with kaniko
kubectl create configmap -n catalogd-e2e --from-file=testdata/catalogs/test-catalog.Dockerfile catalogd-e2e.dockerfile
kubectl create configmap -n catalogd-e2e --from-file=testdata/catalogs/test-catalog catalogd-e2e.build-contents

# Create the kaniko pod to build the test image and push it to the test registry.
# Wait until kaniko pod has reached Phase==Succeeded or we've reached the retry limit
kubectl apply -f test/tools/imageregistry/imagebuilder.yaml
phase=$(kubectl -n catalogd-e2e get pods/kaniko -o=jsonpath='{.status.phase}')
retries=0
while [ "$phase" != "Succeeded" ]
do
  if (( retries >= 5 ))
  then 
    echo "Pod \"catalogd-e2e/kaniko\" has not reached Phase Succeeded. This means the test image has not been successfully built and pushed to the registry. Exiting..."
    exit 1
  fi
  echo "Test image has not been successfully built and pushed to the test registry (Indicated by pod \"catalogd-e2e/kaniko\" having a phase of Succeeded). Retrying status check ..."
  (( retries+=1 ))
  sleep 10
  phase=$(kubectl -n catalogd-e2e get pods/kaniko -o=jsonpath='{.status.phase}')
done

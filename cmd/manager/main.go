/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/metadata"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/go-logr/logr"
	"github.com/spf13/pflag"

	"github.com/operator-framework/catalogd/internal/source"
	"github.com/operator-framework/catalogd/internal/third_party/server"
	"github.com/operator-framework/catalogd/internal/version"
	corecontrollers "github.com/operator-framework/catalogd/pkg/controllers/core"
	"github.com/operator-framework/catalogd/pkg/features"
	catalogdmetrics "github.com/operator-framework/catalogd/pkg/metrics"
	"github.com/operator-framework/catalogd/pkg/profile"
	"github.com/operator-framework/catalogd/pkg/storage"

	//+kubebuilder:scaffold:imports
	"github.com/operator-framework/catalogd/api/core/v1alpha1"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const storageDir = "catalogs"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		profiling            bool
		catalogdVersion      bool
		systemNamespace      string
		catalogServerAddr    string
		httpExternalAddr     string
		cacheDir             string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&systemNamespace, "system-namespace", "", "The namespace catalogd uses for internal state, configuration, and workloads")
	flag.StringVar(&catalogServerAddr, "catalogs-server-addr", ":8083", "The address where the unpacked catalogs' content will be accessible")
	flag.StringVar(&httpExternalAddr, "http-external-address", "http://catalogd-catalogserver.catalogd-system.svc", "The external address at which the http server is reachable.")
	flag.StringVar(&cacheDir, "cache-dir", "/var/cache/", "The directory in the filesystem that catalogd will use for file based caching")
	flag.BoolVar(&profiling, "profiling", false, "enable profiling endpoints to allow for using pprof")
	flag.BoolVar(&catalogdVersion, "version", false, "print the catalogd version and exit")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	// Combine both flagsets and parse them
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	features.CatalogdFeatureGate.AddFlag(pflag.CommandLine)
	pflag.Parse()

	if catalogdVersion {
		fmt.Printf("%#v\n", version.Version())
		os.Exit(0)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "catalogd-operator-lock",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if systemNamespace == "" {
		systemNamespace = podNamespace()
	}

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		setupLog.Error(err, "unable to create cache directory")
		os.Exit(1)
	}

	unpacker, err := source.NewDefaultUnpacker(systemNamespace, cacheDir)
	if err != nil {
		setupLog.Error(err, "unable to create unpacker")
		os.Exit(1)
	}

	var localStorage storage.Instance
	if features.CatalogdFeatureGate.Enabled(features.HTTPServer) {
		metrics.Registry.MustRegister(catalogdmetrics.RequestDurationMetric)

		if err := os.MkdirAll(filepath.Join(cacheDir, storageDir), 0700); err != nil {
			setupLog.Error(err, "unable to create storage directory for catalogs")
			os.Exit(1)
		}

		baseStorageURL, err := url.Parse(fmt.Sprintf("%s/catalogs/", httpExternalAddr))
		if err != nil {
			setupLog.Error(err, "unable to create base storage URL")
			os.Exit(1)
		}
		localStorage = storage.LocalDir{RootDir: filepath.Join(cacheDir, storageDir), BaseURL: baseStorageURL}
		shutdownTimeout := 30 * time.Second
		catalogServer := server.Server{
			Kind: "catalogs",
			Server: &http.Server{
				Addr:         catalogServerAddr,
				Handler:      catalogdmetrics.AddMetricsToHandler(localStorage.StorageServerHandler()),
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
			ShutdownTimeout: &shutdownTimeout,
		}

		if err := mgr.Add(&catalogServer); err != nil {
			setupLog.Error(err, "unable to start catalog server")
			os.Exit(1)
		}
	}

	if err = (&corecontrollers.CatalogReconciler{
		Client:   mgr.GetClient(),
		Unpacker: unpacker,
		Storage:  localStorage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Catalog")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if profiling {
		pprofer := profile.NewPprofer()
		if err := pprofer.ConfigureControllerManager(mgr); err != nil {
			setupLog.Error(err, "unable to setup pprof configuration")
			os.Exit(1)
		}
	}

	metaClient, err := metadata.NewForConfig(cfg)
	if err != nil {
		setupLog.Error(err, "unable to setup client for garbage collection")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	if err := unpackStartupGarbageCollection(ctx, filepath.Join(cacheDir, source.UnpackCacheDir), setupLog, metaClient); err != nil {
		setupLog.Error(err, "running garbage collection")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func podNamespace() string {
	namespace, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "catalogd-system"
	}
	return string(namespace)
}

func unpackStartupGarbageCollection(ctx context.Context, cachePath string, log logr.Logger, metaClient metadata.Interface) error {
	getter := metaClient.Resource(v1alpha1.GroupVersion.WithResource("catalogs"))
	metaList, err := getter.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing catalogs: %w", err)
	}

	expectedCatalogs := sets.New[string]()
	for _, meta := range metaList.Items {
		expectedCatalogs.Insert(meta.GetName())
	}

	cacheDirEntries, err := os.ReadDir(cachePath)
	if err != nil {
		return fmt.Errorf("error reading cache directory: %w", err)
	}
	for _, cacheDirEntry := range cacheDirEntries {
		if cacheDirEntry.IsDir() && expectedCatalogs.Has(cacheDirEntry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(cachePath, cacheDirEntry.Name())); err != nil {
			log.Error(err, "error removing cache directory entry", "path", cacheDirEntry.Name(), "isDir", cacheDirEntry.IsDir())
		} else {
			log.Info("deleted unexpected cache directory entry", "path", cacheDirEntry.Name(), "isDir", cacheDirEntry.IsDir())
		}
	}
	return nil
}

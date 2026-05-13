package main

import (
	"flag"
	"os"
	"time"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/giantswarm/hr-recovery-controller/internal/controller"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(helmv2.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr        string
		probeAddr          string
		leaderElect        bool
		maxPokes           int
		backoff            time.Duration
		namespacePrefix    string
		watchLabelSelector string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the health probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election.")
	flag.IntVar(&maxPokes, "max-pokes", 10, "Maximum number of pokes per HelmRelease before giving up.")
	flag.DurationVar(&backoff, "backoff", 5*time.Minute, "Minimum interval between pokes for the same HelmRelease.")
	flag.StringVar(&namespacePrefix, "namespace-prefix", "", "If set, only act on HelmReleases in namespaces with this prefix.")
	flag.StringVar(&watchLabelSelector, "watch-label-selector", "", "If set, only watch HelmReleases matching this label selector.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)

	selector := labels.Everything()
	if watchLabelSelector != "" {
		s, err := labels.Parse(watchLabelSelector)
		if err != nil {
			ctrl.Log.Error(err, "invalid --watch-label-selector")
			os.Exit(1)
		}
		selector = s
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "hr-recovery-controller.giantswarm.io",
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up readiness check")
		os.Exit(1)
	}

	r := &controller.Reconciler{
		Client:          mgr.GetClient(),
		Recorder:        mgr.GetEventRecorderFor("hr-recovery-controller"),
		MaxPokes:        maxPokes,
		Backoff:         backoff,
		NamespacePrefix: namespacePrefix,
		LabelSelector:   selector,
	}
	if err := r.SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to set up reconciler")
		os.Exit(1)
	}

	ctrl.Log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "manager exited")
		os.Exit(1)
	}
}

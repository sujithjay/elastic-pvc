package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"elastic-pvc/internal/controller"
	"elastic-pvc/internal/kubelet"
)

var cfg struct {
	metricsAddr        string
	healthAddr         string
	watchInterval      time.Duration
	maxResizesPerCycle int
	resizeCooldown     time.Duration
	development        bool
}

var rootCmd = &cobra.Command{
	Use:   "elastic-pvc",
	Short: "Automatic PVC resizer for EBS volumes",
	Long: `elastic-pvc monitors PVC filesystem usage via kubelet stats and automatically
expands EBS-backed PersistentVolumeClaims when free space drops below a threshold.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		return run()
	},
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	fs := rootCmd.Flags()
	fs.StringVar(&cfg.metricsAddr, "metrics-addr", ":8080", "Address for the metrics endpoint")
	fs.StringVar(&cfg.healthAddr, "health-addr", ":8081", "Address for health/readiness probes")
	fs.DurationVar(&cfg.watchInterval, "interval", 1*time.Minute, "Interval between resize checks")
	fs.IntVar(&cfg.maxResizesPerCycle, "max-resizes-per-cycle", 10, "Maximum number of resize operations per reconciliation cycle")
	fs.DurationVar(&cfg.resizeCooldown, "resize-cooldown", 5*time.Minute, "Minimum interval between resizes for the same PVC")
	fs.BoolVar(&cfg.development, "development", false, "Enable development logging")
}

func run() error {
	opts := zap.Options{Development: cfg.development}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	restConfig := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Metrics: metricsserver.Options{
			BindAddress: cfg.metricsAddr,
		},
		HealthProbeBindAddress: cfg.healthAddr,
	})
	if err != nil {
		return fmt.Errorf("creating manager: %w", err)
	}

	if err := controller.SetupIndexer(mgr); err != nil {
		return fmt.Errorf("setting up indexers: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	autoscaler := controller.NewAutoscaler(
		kubelet.NewStatsClient(clientset),
		mgr.GetClient(),
		mgr.GetEventRecorderFor("elastic-pvc"),
		cfg.watchInterval,
		cfg.maxResizesPerCycle,
		cfg.resizeCooldown,
	)
	if err := mgr.Add(autoscaler); err != nil {
		return fmt.Errorf("adding autoscaler: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("setting up healthz: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("setting up readyz: %w", err)
	}

	log.Info("starting elastic-pvc", "interval", cfg.watchInterval)
	return mgr.Start(ctrl.SetupSignalHandler())
}

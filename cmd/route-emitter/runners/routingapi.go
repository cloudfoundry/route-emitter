package runners

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"

	"code.cloudfoundry.org/routing-api"
	apiconfig "code.cloudfoundry.org/routing-api/config"
	"code.cloudfoundry.org/routing-api/models"
	"github.com/tedsuo/ifrit/ginkgomon"
)

type Config struct {
	apiconfig.Config
	DevMode bool
	IP      string
}

type RoutingAPIRunner struct {
	Config              Config
	configPath, binPath string
}

type SQLConfig struct {
	Port       int
	DBName     string
	DriverName string
	Username   string
	Password   string
}

func NewRoutingAPIRunner(binPath string, adminPort int, sqlConfig SQLConfig, fs ...func(*Config)) (*RoutingAPIRunner, error) {
	cfg := Config{
		DevMode: true,
		Config: apiconfig.Config{
			AdminPort: adminPort,
			// required fields
			MetricsReportingIntervalString:  "500ms",
			StatsdClientFlushIntervalString: "10ms",
			SystemDomain:                    "example.com",
			LogGuid:                         "routing-api-logs",
			RouterGroups: models.RouterGroups{
				{
					Name:            "default-tcp",
					Type:            "tcp",
					ReservablePorts: "1024-65535",
				},
			},
			// end of required fields
			SqlDB: apiconfig.SqlDB{
				Host:     "localhost",
				Port:     sqlConfig.Port,
				Schema:   sqlConfig.DBName,
				Type:     sqlConfig.DriverName,
				Username: sqlConfig.Username,
				Password: sqlConfig.Password,
			},
			UUID: "routing-api-uuid",
		},
	}

	for _, f := range fs {
		f(&cfg)
	}

	f, err := ioutil.TempFile(os.TempDir(), "routing-api-config")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	configBytes, err := yaml.Marshal(cfg.Config)
	if err != nil {
		return nil, err
	}
	_, err = f.Write(configBytes)
	if err != nil {
		return nil, err
	}

	return &RoutingAPIRunner{
		Config:     cfg,
		configPath: f.Name(),
		binPath:    binPath,
	}, nil
}

func (runner *RoutingAPIRunner) Run(signals <-chan os.Signal, ready chan<- struct{}) error {
	// Create a new ginkgomon runner here instead in New() so that we can restart
	// the same runner without having to worry about messing the state of the
	// ginkgomon Runner
	args := []string{
		"-ip", "localhost",
		"-config", runner.configPath,
		"-logLevel=debug",
		"-devMode=" + strconv.FormatBool(runner.Config.DevMode),
	}
	r := ginkgomon.New(ginkgomon.Config{
		Name:              "routing-api",
		Command:           exec.Command(runner.binPath, args...),
		StartCheck:        "routing-api.started",
		StartCheckTimeout: 20 * time.Second,
	})
	return r.Run(signals, ready)
}

func (runner *RoutingAPIRunner) GetGUID() (string, error) {
	client := routing_api.NewClient(fmt.Sprintf("http://127.0.0.1:%d", runner.Config.API.ListenPort), false)
	routerGroups, err := client.RouterGroups()
	if err != nil {
		return "", err
	}

	return routerGroups[0].Guid, nil
}

func (runner *RoutingAPIRunner) GetClient() routing_api.Client {
	return routing_api.NewClient(fmt.Sprintf("http://127.0.0.1:%d", runner.Config.API.ListenPort), false)
}

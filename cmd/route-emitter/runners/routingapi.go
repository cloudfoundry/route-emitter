package runners

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"time"

	"gopkg.in/yaml.v2"

	"code.cloudfoundry.org/bbs/test_helpers/sqlrunner"
	"code.cloudfoundry.org/localip"
	"code.cloudfoundry.org/routing-api"
	apiconfig "code.cloudfoundry.org/routing-api/config"
	"code.cloudfoundry.org/routing-api/models"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

type Config struct {
	apiconfig.Config
	DevMode bool
	IP      string
	Port    int
}

type RoutingAPIRunner struct {
	ifrit.Runner
	Config Config
}

func NewRoutingAPIRunner(binPath, consulURL string, sqlRunner sqlrunner.SQLRunner, fs ...func(*Config)) (*RoutingAPIRunner, error) {
	port, err := localip.LocalPort()
	if err != nil {
		return nil, err
	}

	cfg := Config{
		Port:    int(port),
		DevMode: true,
		Config: apiconfig.Config{
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
			ConsulCluster: apiconfig.ConsulCluster{
				Servers:       consulURL,
				RetryInterval: 50 * time.Millisecond,
			},
			SqlDB: apiconfig.SqlDB{
				Host:     "localhost",
				Port:     sqlRunner.Port(),
				Schema:   sqlRunner.DBName(),
				Type:     sqlRunner.DriverName(),
				Username: sqlRunner.Username(),
				Password: sqlRunner.Password(),
			},
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

	args := []string{
		"-port", strconv.Itoa(int(cfg.Port)),
		"-ip", "localhost",
		"-config", f.Name(),
		"-logLevel=debug",
		"-devMode=" + strconv.FormatBool(cfg.DevMode),
	}
	return &RoutingAPIRunner{
		Config: cfg,
		Runner: ginkgomon.New(ginkgomon.Config{
			Name:              "routing-api",
			Command:           exec.Command(binPath, args...),
			StartCheck:        "routing-api.started",
			StartCheckTimeout: 10 * time.Second,
		}),
	}, nil
}

func (runner *RoutingAPIRunner) GetGUID() (string, error) {
	client := routing_api.NewClient(fmt.Sprintf("http://127.0.0.1:%d", runner.Config.Port), false)
	routerGroups, err := client.RouterGroups()
	if err != nil {
		return "", err
	}

	return routerGroups[0].Guid, nil
}
func (runner *RoutingAPIRunner) GetClient() routing_api.Client {
	return routing_api.NewClient(fmt.Sprintf("http://127.0.0.1:%d", runner.Config.Port), false)
}

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"
	"strings"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/universal-translator"
	"github.com/juju/errors"
	"github.com/msiedlarek/nifi_exporter/nifi/client"
	"github.com/msiedlarek/nifi_exporter/nifi/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"gopkg.in/go-playground/validator.v9"
	validator_en "gopkg.in/go-playground/validator.v9/translations/en"
	"gopkg.in/yaml.v2"
)

type Configuration struct {
	Exporter struct {
		ListenAddress string `yaml:"listenAddress" validate:"required"`
	} `yaml:"exporter" validate:"required"`
	NiFi struct {
		URL      string `yaml:"url" validate:"required,url"`
		Username string `yaml:"username" validate:"required"`
		Password string `yaml:"password" validate:"required"`
	} `yaml:"nifi" validate:"required"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s CONFIG_PATH", os.Args[0])
		os.Exit(2)
	}
	configPath := os.Args[1]

	log.Info("Starting nifi_exporter...")

	config, err := loadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}

	if err := start(config); err != nil {
		log.Fatal(err)
	}
}

func loadConfig(configPath string) (*Configuration, error) {
	log.WithField("path", configPath).Info("Loading configuration file...")

	configYaml, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, errors.Annotate(err, "Couldn't read config file")
	}

	var config Configuration
	if err := yaml.Unmarshal(configYaml, &config); err != nil {
		return nil, errors.Annotate(err, "Couldn't parse config file")
	}

	locale := en.New()
	universalTranslator := ut.New(locale, locale)
	translator, found := universalTranslator.GetTranslator(locale.Locale())
	if !found {
		return nil, errors.New("Couldn't initialize validation error translator")
	}

	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		return field.Tag.Get("yaml")
	})
	validator_en.RegisterDefaultTranslations(validate, translator)

	if err := validate.Struct(&config); err != nil {
		validationErrors := err.(validator.ValidationErrors)
		for i := range validationErrors {
			fieldError := validationErrors[i]
			log.WithFields(log.Fields{
				"field": strings.SplitN(fieldError.Namespace(), ".", 2)[1],
				"error": fieldError.Translate(translator),
			}).Error("Invalid configuration.")
		}
		return nil, errors.New("Configuration file validation failed.")
	}

	log.WithField("path", configPath).Info("Configuration file successfully loaded.")

	return &config, nil
}

func start(config *Configuration) error {
	log.WithFields(log.Fields{
		"url":      config.NiFi.URL,
		"username": config.NiFi.Username,
	}).Info("Connecting to NiFi...")

	c := client.NewClient(config.NiFi.URL, config.NiFi.Username, config.NiFi.Password)

	if err := prometheus.DefaultRegisterer.Register(collectors.NewDiagnosticsCollector(c)); err != nil {
		return errors.Annotate(err, "Couldn't register system diagnostics collector.")
	}
	if err := prometheus.DefaultRegisterer.Register(collectors.NewCountersCollector(c)); err != nil {
		return errors.Annotate(err, "Couldn't register counters collector.")
	}
	if err := prometheus.DefaultRegisterer.Register(collectors.NewProcessGroupsCollector(c)); err != nil {
		return errors.Annotate(err, "Couldn't register process groups collector.")
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>NiFi Exporter</title></head>
<body>
<h1>NiFi Exporter</h1>
<p><a href="/metrics">Metrics</a></p>
</body>
</html>`))
	})
	http.Handle("/metrics", promhttp.Handler())

	log.WithField("address", config.Exporter.ListenAddress).Infof(
		"Listening on: http://%s/metrics",
		config.Exporter.ListenAddress,
	)
	if err := http.ListenAndServe(config.Exporter.ListenAddress, nil); err != nil {
		return errors.Annotate(err, "Couldn't start HTTP server.")
	}
	return nil
}
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v2"
)

// Define the structure to match the YAML file
type Rule struct {
	Record string `yaml:"record"`
	Expr   string `yaml:"expr"`
}

type Group struct {
	Target   string `yaml:"target"`
	Rules    []Rule `yaml:"rules"`
	Endpoint string `yaml:"endpoint"`
}

type Config struct {
	Targets map[string]Group `yaml:"targets"`
}

var (
	configFile    = "rules.yaml"
	prometheusURL = "http://localhost:9090"
	ruleMetrics   = map[string]*prometheus.GaugeVec{}
)

func loadConfig() (Config, error) {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		return Config{}, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return Config{}, err
	}

	return config, nil
}

func queryPrometheus(endpoint string, query string) ([]map[string]interface{}, error) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/query?query=%s", endpoint, query))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, err
	}

	results := result["data"].(map[string]interface{})["result"].([]interface{})
	var parsedResults []map[string]interface{}

	for _, res := range results {
		parsedResult := res.(map[string]interface{})
		labels := parsedResult["metric"].(map[string]interface{})
		value := parsedResult["value"].([]interface{})[1].(string)
		labels["value"] = value
		parsedResults = append(parsedResults, labels)
	}

	return parsedResults, nil
}

func handler(config Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "Missing target parameter", http.StatusBadRequest)
			return
		}

		group, exists := config.Targets[target]
		if !exists {
			http.Error(w, "Target not found", http.StatusNotFound)
			return
		}

		for _, rule := range group.Rules {
			results, err := queryPrometheus(group.Endpoint, rule.Expr)
			if err != nil {
				log.Printf("Error querying Prometheus for rule %s: %v", rule.Record, err)
				continue
			}

			for _, result := range results {
				value, _ := strconv.ParseFloat(result["value"].(string), 64)
				labels := make(prometheus.Labels)
				for k, v := range result {
					if k != "value" {
						labels[k] = v.(string)
					}
				}

				metric, exists := ruleMetrics[rule.Record]
				if !exists {
					metricVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
						Name: rule.Record,
						Help: fmt.Sprintf("Value of Prometheus query: %s", rule.Expr),
					}, getLabelNames(labels))
					prometheus.MustRegister(metricVec)
					ruleMetrics[rule.Record] = metricVec
					metric = metricVec
				}

				metric.With(labels).Set(value)
			}
		}

		promhttp.Handler().ServeHTTP(w, r)
	}
}

func getLabelNames(labels prometheus.Labels) []string {
	var labelNames []string
	for k := range labels {
		labelNames = append(labelNames, k)
	}
	return labelNames
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	http.Handle("/probe", handler(config))
	log.Fatal(http.ListenAndServe(":8080", nil))
}

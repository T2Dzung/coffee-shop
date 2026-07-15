package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const writeOperationsMetricName = "coffeeshop_operator_write_operations_total"

type writeOperation string
type writeResource string

const (
	writeOperationApply       writeOperation = "apply"
	writeOperationApplyDryRun writeOperation = "apply_dry_run"
	writeOperationDelete      writeOperation = "delete"
	writeOperationStatusPatch writeOperation = "status_patch"

	writeResourceDeployment    writeResource = "deployment"
	writeResourceService       writeResource = "service"
	writeResourceCoffeeShopSvc writeResource = "coffeeshopservice"
	writeResultSuccess                       = "success"
	writeResultError                         = "error"
)

var writeOperations = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: writeOperationsMetricName,
		Help: "Total Kubernetes write requests made by the CoffeeShopService controller.",
	},
	[]string{"operation", "resource", "result"},
)

func init() {
	crmetrics.Registry.MustRegister(writeOperations)
}

func recordWrite(operation writeOperation, resource writeResource, err error) {
	result := writeResultSuccess
	if err != nil {
		result = writeResultError
	}
	writeOperations.WithLabelValues(string(operation), string(resource), result).Inc()
}

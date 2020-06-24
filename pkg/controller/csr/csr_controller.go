// Copyright (c) 2020 Red Hat, Inc.

package csr

import (
	"context"
	"fmt"

	clusterv1 "github.com/open-cluster-management/api/cluster/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	libgoclient "github.com/open-cluster-management/library-go/pkg/client"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
)

const (
	userNameSignature = "system:serviceaccount:%s:%s-bootstrap-sa"
	clusterLabel      = "open-cluster-management.io/cluster-name"
)

var log = logf.Log.WithName("controller_csr")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new ManagedCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	kubeClient, err := libgoclient.NewDefaultKubeClient("")
	if err != nil {
		kubeClient = nil
	}
	return &ReconcileCSR{client: mgr.GetClient(), kubeClient: kubeClient, scheme: mgr.GetScheme()}
}

func getClusterName(csr *certificatesv1beta1.CertificateSigningRequest) (clusterName string) {
	for label, v := range csr.GetObjectMeta().GetLabels() {
		if label == clusterLabel {
			clusterName = v
		}
	}
	return clusterName
}

func getApprovalType(csr *certificatesv1beta1.CertificateSigningRequest) string {
	if csr.Status.Conditions == nil {
		return ""
	}
	for _, c := range csr.Status.Conditions {
		if c.Type == certificatesv1beta1.CertificateApproved || c.Type == certificatesv1beta1.CertificateDenied {
			return string(c.Type)
		}
	}
	return ""
}

func validUsername(csr *certificatesv1beta1.CertificateSigningRequest, clusterName string) bool {
	return csr.Spec.Username == fmt.Sprintf(userNameSignature, clusterName, clusterName)
}

func csrPredicate(csr *certificatesv1beta1.CertificateSigningRequest) bool {
	clusterName := getClusterName(csr)
	return clusterName != "" &&
		getApprovalType(csr) == "" &&
		validUsername(csr, clusterName)
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("csr-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	csrPredicateFuncs := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return csrPredicate(e.ObjectNew.(*certificatesv1beta1.CertificateSigningRequest))
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return csrPredicate(e.Object.(*certificatesv1beta1.CertificateSigningRequest))
		},
	}

	// Watch for changes to primary resource ManagedCluster
	err = c.Watch(
		&source.Kind{Type: &certificatesv1beta1.CertificateSigningRequest{}},
		&handler.EnqueueRequestForObject{},
		csrPredicateFuncs,
	)

	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileCSR implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileCSR{}

// ReconcileCSR reconciles a ReconcileCSR object
type ReconcileCSR struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client     client.Client
	kubeClient kubernetes.Interface
	scheme     *runtime.Scheme
}

// Reconcile reads that state of the csr for a ReconcileCSR object and makes changes based on the state read
// and what is in the CertificateSigningRequest.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileCSR) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling CSR")

	// Fetch the CertificateSigningRequest instance
	instance := &certificatesv1beta1.CertificateSigningRequest{}

	if err := r.client.Get(context.TODO(), request.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if instance.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	clusterName := getClusterName(instance)

	cluster := clusterv1.ManagedCluster{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: clusterName}, &cluster)
	if err != nil {
		reqLogger.Info("Warning", "error", err.Error())
		return reconcile.Result{}, nil
	}

	reqLogger.Info("Approving CSR", "name", instance.Name)
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = make([]certificatesv1beta1.CertificateSigningRequestCondition, 0)
	}

	instance.Status.Conditions = append(instance.Status.Conditions, certificatesv1beta1.CertificateSigningRequestCondition{
		Type:           certificatesv1beta1.CertificateApproved,
		Reason:         "AutoApprovedByCSRController",
		Message:        "The managedcluster-import-controller auto approval automatically approved this CSR",
		LastUpdateTime: metav1.Now(),
	})

	signingRequest := r.kubeClient.CertificatesV1beta1().CertificateSigningRequests()
	if _, err := signingRequest.UpdateApproval(context.TODO(), instance, metav1.UpdateOptions{}); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

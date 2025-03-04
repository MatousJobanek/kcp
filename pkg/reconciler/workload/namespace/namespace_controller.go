/*
Copyright 2021 The KCP Authors.

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

package namespace

import (
	"context"
	"fmt"
	"time"

	"github.com/kcp-dev/logicalcluster"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clusters"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	tenancyinformers "github.com/kcp-dev/kcp/pkg/client/informers/externalversions/tenancy/v1alpha1"
	workloadinformer "github.com/kcp-dev/kcp/pkg/client/informers/externalversions/workload/v1alpha1"
	tenancylisters "github.com/kcp-dev/kcp/pkg/client/listers/tenancy/v1alpha1"
	workloadlisters "github.com/kcp-dev/kcp/pkg/client/listers/workload/v1alpha1"
)

const controllerName = "kcp-workload-namespace"

// NewController returns a new Controller which schedules namespaced resources to a Cluster.
func NewController(
	kubeClusterClient kubernetes.ClusterInterface,
	workspaceInformer tenancyinformers.ClusterWorkspaceInformer,
	clusterInformer workloadinformer.WorkloadClusterInformer,
	clusterLister workloadlisters.WorkloadClusterLister,
	namespaceInformer coreinformers.NamespaceInformer,
	namespaceLister corelisters.NamespaceLister,
) *Controller {
	namespaceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerName+"-namespace")
	clusterQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerName+"-cluster")
	workspaceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerName+"-workspace")

	workspaceLister := workspaceInformer.Lister()

	c := &Controller{
		namespaceQueue: namespaceQueue,
		clusterQueue:   clusterQueue,
		workspaceQueue: workspaceQueue,

		workspaceLister: workspaceLister,
		clusterLister:   clusterLister,
		namespaceLister: namespaceLister,
		kubeClient:      kubeClusterClient,
	}

	clusterInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueCluster(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueueCluster(obj) },
		DeleteFunc: func(obj interface{}) { c.enqueueCluster(obj) },
	})

	namespaceInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: filterNamespace,
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { c.enqueueNamespace(obj) },
			UpdateFunc: func(_, obj interface{}) { c.enqueueNamespace(obj) },
			DeleteFunc: nil, // Nothing to do.
		},
	})

	workspaceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueWorkspace(obj) },
		UpdateFunc: func(_, obj interface{}) { c.enqueueWorkspace(obj) },
		DeleteFunc: nil, // Nothing to do.
	})

	return c
}

type Controller struct {
	namespaceQueue workqueue.RateLimitingInterface
	clusterQueue   workqueue.RateLimitingInterface
	workspaceQueue workqueue.RateLimitingInterface

	clusterLister   workloadlisters.WorkloadClusterLister
	namespaceLister corelisters.NamespaceLister
	workspaceLister tenancylisters.ClusterWorkspaceLister
	kubeClient      kubernetes.ClusterInterface
}

func filterNamespace(obj interface{}) bool {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return false
	}
	_, clusterAwareName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(err)
		return false
	}
	_, name := clusters.SplitClusterAwareKey(clusterAwareName)
	if namespaceBlocklist.Has(name) {
		klog.V(2).Infof("Skipping syncing namespace %q", name)
		return false
	}
	return true
}

func (c *Controller) enqueueNamespace(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.namespaceQueue.Add(key)
}

func (c *Controller) enqueueCluster(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.clusterQueue.Add(key)
}

func (c *Controller) enqueueClusterAfter(obj interface{}, dur time.Duration) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.clusterQueue.AddAfter(key, dur)
}

func (c *Controller) enqueueWorkspace(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.workspaceQueue.Add(key)
}

func (c *Controller) Start(ctx context.Context, numThreads int) {
	defer runtime.HandleCrash()
	defer c.namespaceQueue.ShutDown()
	defer c.clusterQueue.ShutDown()
	defer c.workspaceQueue.ShutDown()

	klog.Info("Starting Namespace scheduler")
	defer klog.Info("Shutting down Namespace scheduler")

	for i := 0; i < numThreads; i++ {
		go wait.Until(func() { c.startNamespaceWorker(ctx) }, time.Second, ctx.Done())
		go wait.Until(func() { c.startClusterWorker(ctx) }, time.Second, ctx.Done())
		go wait.Until(func() { c.startWorkspaceWorker(ctx) }, time.Second, ctx.Done())
	}
	<-ctx.Done()
}

func (c *Controller) startNamespaceWorker(ctx context.Context) {
	for processNext(ctx, c.namespaceQueue, c.processNamespace) {
	}
}
func (c *Controller) startClusterWorker(ctx context.Context) {
	for processNext(ctx, c.clusterQueue, c.processCluster) {
	}
}

func (c *Controller) startWorkspaceWorker(ctx context.Context) {
	for processNext(ctx, c.workspaceQueue, c.processWorkspace) {
	}
}

func processNext(
	ctx context.Context,
	queue workqueue.RateLimitingInterface,
	processFunc func(ctx context.Context, key string) error,
) bool {
	// Wait until there is a new item in the working  queue
	k, quit := queue.Get()
	if quit {
		return false
	}
	key := k.(string)

	// No matter what, tell the queue we're done with this key, to unblock
	// other workers.
	defer queue.Done(key)

	if err := processFunc(ctx, key); err != nil {
		runtime.HandleError(fmt.Errorf("%q controller failed to sync %q, err: %w", controllerName, key, err))
		queue.AddRateLimited(key)
		return true
	}
	queue.Forget(key)
	return true
}

// namespaceBlocklist holds a set of namespaces that should never be synced from kcp to physical clusters.
var namespaceBlocklist = sets.NewString("kube-system", "kube-public", "kube-node-lease")

func (c *Controller) processNamespace(ctx context.Context, key string) error {
	ns, err := c.namespaceLister.Get(key)
	if k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	// Get logical cluster name.
	_, clusterAwareName, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		klog.Errorf("failed to split key %q, dropping: %v", key, err)
		return nil
	}
	lclusterName, _ := clusters.SplitClusterAwareKey(clusterAwareName)

	return c.reconcileNamespace(ctx, lclusterName, ns.DeepCopy())
}

func (c *Controller) processCluster(ctx context.Context, key string) error {
	cluster, err := c.clusterLister.Get(key)
	if k8serrors.IsNotFound(err) {
		// A deleted cluster requires evaluating all namespaces for
		// potential rescheduling.
		//
		// TODO(marun) Consider using a cluster finalizer to speed up
		// convergence if cluster deletion events are missed by this
		// controller. Rescheduling will always happen eventually due
		// to namespace informer resync.

		clusterName, _ := clusters.SplitClusterAwareKey(key)

		return c.enqueueNamespaces(clusterName, labels.Everything())
	} else if err != nil {
		return err
	}

	return c.observeCluster(ctx, cluster.DeepCopy())
}

func (c *Controller) processWorkspace(ctx context.Context, key string) error {
	workspace, err := c.workspaceLister.Get(key)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Ensure any workspace changes result in contained namespaces being enqueued
	// for possible rescheduling.

	clusterName := logicalcluster.From(workspace)

	return c.enqueueNamespaces(clusterName, labels.Everything())
}

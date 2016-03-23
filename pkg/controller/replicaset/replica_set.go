/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

// If you make changes to this file, you should also make the corresponding change in ReplicationController.

package replicaset

import (
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/record"
	unversioned_core "k8s.io/kubernetes/pkg/client/typed/generated/core/unversioned"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	utilruntime "k8s.io/kubernetes/pkg/util/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/util/workqueue"
	"k8s.io/kubernetes/pkg/watch"
)

const (
	// We'll attempt to recompute the required replicas of all ReplicaSets
	// that have fulfilled their expectations at least this often. This recomputation
	// happens based on contents in local pod storage.
	FullControllerResyncPeriod = 30 * time.Second

	// Realistic value of the burstReplica field for the replication manager based off
	// performance requirements for kubernetes 1.0.
	BurstReplicas = 500

	// We must avoid counting pods until the pod store has synced. If it hasn't synced, to
	// avoid a hot loop, we'll wait this long between checks.
	PodStoreSyncedPollPeriod = 100 * time.Millisecond

	// The number of times we retry updating a ReplicaSet's status.
	statusUpdateRetries = 1
)

// ReplicaSetController is responsible for synchronizing ReplicaSet objects stored
// in the system with actual running pods.
type ReplicaSetController struct {
	kubeClient clientset.Interface
	podControl controller.PodControlInterface

	// A ReplicaSet is temporarily suspended after creating/deleting these many replicas.
	// It resumes normal action after observing the watch events for them.
	burstReplicas int
	// To allow injection of syncReplicaSet for testing.
	syncHandler func(rsKey string) error

	// A TTLCache of pod creates/deletes each ReplicaSet expects to see
	expectations controller.ControllerExpectationsInterface

	// A store of ReplicaSets, populated by the rsController
	rsStore cache.StoreToReplicaSetLister
	// Watches changes to all ReplicaSets
	rsController *framework.Controller
	// A store of pods, populated by the podController
	podStore cache.StoreToPodLister
	// Watches changes to all pods
	podController *framework.Controller
	// podStoreSynced returns true if the pod store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	podStoreSynced func() bool

	// Controllers that need to be synced
	queue *workqueue.Type
}

// NewReplicaSetController creates a new ReplicaSetController.
func NewReplicaSetController(kubeClient clientset.Interface, resyncPeriod controller.ResyncPeriodFunc, burstReplicas int) *ReplicaSetController {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&unversioned_core.EventSinkImpl{kubeClient.Core().Events("")})

	rsc := &ReplicaSetController{
		kubeClient: kubeClient,
		podControl: controller.RealPodControl{
			KubeClient: kubeClient,
			Recorder:   eventBroadcaster.NewRecorder(api.EventSource{Component: "replicaset-controller"}),
		},
		burstReplicas: burstReplicas,
		expectations:  controller.NewControllerExpectations(),
		queue:         workqueue.New(),
	}

	rsc.rsStore.Store, rsc.rsController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return rsc.kubeClient.Extensions().ReplicaSets(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return rsc.kubeClient.Extensions().ReplicaSets(api.NamespaceAll).Watch(options)
			},
		},
		&extensions.ReplicaSet{},
		// TODO: Can we have much longer period here?
		FullControllerResyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc: rsc.enqueueReplicaSet,
			UpdateFunc: func(old, cur interface{}) {
				// You might imagine that we only really need to enqueue the
				// replica set when Spec changes, but it is safer to sync any
				// time this function is triggered. That way a full informer
				// resync can requeue any replica set that don't yet have pods
				// but whose last attempts at creating a pod have failed (since
				// we don't block on creation of pods) instead of those
				// replica sets stalling indefinitely. Enqueueing every time
				// does result in some spurious syncs (like when Status.Replica
				// is updated and the watch notification from it retriggers
				// this function), but in general extra resyncs shouldn't be
				// that bad as ReplicaSets that haven't met expectations yet won't
				// sync, and all the listing is done using local stores.
				oldRS := old.(*extensions.ReplicaSet)
				curRS := cur.(*extensions.ReplicaSet)
				if oldRS.Status.Replicas != curRS.Status.Replicas {
					glog.V(4).Infof("Observed updated replica count for ReplicaSet: %v, %d->%d", curRS.Name, oldRS.Status.Replicas, curRS.Status.Replicas)
				}
				rsc.enqueueReplicaSet(cur)
			},
			// This will enter the sync loop and no-op, because the replica set has been deleted from the store.
			// Note that deleting a replica set immediately after scaling it to 0 will not work. The recommended
			// way of achieving this is by performing a `stop` operation on the replica set.
			DeleteFunc: rsc.enqueueReplicaSet,
		},
	)

	rsc.podStore.Store, rsc.podController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc: func(options api.ListOptions) (runtime.Object, error) {
				return rsc.kubeClient.Core().Pods(api.NamespaceAll).List(options)
			},
			WatchFunc: func(options api.ListOptions) (watch.Interface, error) {
				return rsc.kubeClient.Core().Pods(api.NamespaceAll).Watch(options)
			},
		},
		&api.Pod{},
		resyncPeriod(),
		framework.ResourceEventHandlerFuncs{
			AddFunc: rsc.addPod,
			// This invokes the ReplicaSet for every pod change, eg: host assignment. Though this might seem like
			// overkill the most frequent pod update is status, and the associated ReplicaSet will only list from
			// local storage, so it should be ok.
			UpdateFunc: rsc.updatePod,
			DeleteFunc: rsc.deletePod,
		},
	)

	rsc.syncHandler = rsc.syncReplicaSet
	rsc.podStoreSynced = rsc.podController.HasSynced
	return rsc
}

// SetEventRecorder replaces the event recorder used by the ReplicaSetController
// with the given recorder. Only used for testing.
func (rsc *ReplicaSetController) SetEventRecorder(recorder record.EventRecorder) {
	// TODO: Hack. We can't cleanly shutdown the event recorder, so benchmarks
	// need to pass in a fake.
	rsc.podControl = controller.RealPodControl{KubeClient: rsc.kubeClient, Recorder: recorder}
}

// Run begins watching and syncing.
func (rsc *ReplicaSetController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	go rsc.rsController.Run(stopCh)
	go rsc.podController.Run(stopCh)
	for i := 0; i < workers; i++ {
		go wait.Until(rsc.worker, time.Second, stopCh)
	}
	<-stopCh
	glog.Infof("Shutting down ReplicaSet Controller")
	rsc.queue.ShutDown()
}

// getPodReplicaSet returns the replica set managing the given pod.
// TODO: Surface that we are ignoring multiple replica sets for a single pod.
func (rsc *ReplicaSetController) getPodReplicaSet(pod *api.Pod) *extensions.ReplicaSet {
	rss, err := rsc.rsStore.GetPodReplicaSets(pod)
	if err != nil {
		glog.V(4).Infof("No ReplicaSets found for pod %v, ReplicaSet controller will avoid syncing", pod.Name)
		return nil
	}
	// In theory, overlapping ReplicaSets is user error. This sorting will not prevent
	// oscillation of replicas in all cases, eg:
	// rs1 (older rs): [(k1=v1)], replicas=1 rs2: [(k2=v2)], replicas=2
	// pod: [(k1:v1), (k2:v2)] will wake both rs1 and rs2, and we will sync rs1.
	// pod: [(k2:v2)] will wake rs2 which creates a new replica.
	if len(rss) > 1 {
		// More than two items in this list indicates user error. If two replicasets
		// overlap, sort by creation timestamp, subsort by name, then pick
		// the first.
		glog.Errorf("user error! more than one ReplicaSet is selecting pods with labels: %+v", pod.Labels)
		sort.Sort(overlappingReplicaSets(rss))
	}
	return &rss[0]
}

// When a pod is created, enqueue the replica set that manages it and update it's expectations.
func (rsc *ReplicaSetController) addPod(obj interface{}) {
	pod := obj.(*api.Pod)
	if pod.DeletionTimestamp != nil {
		// on a restart of the controller manager, it's possible a new pod shows up in a state that
		// is already pending deletion. Prevent the pod from being a creation observation.
		rsc.deletePod(pod)
		return
	}
	if rs := rsc.getPodReplicaSet(pod); rs != nil {
		rsKey, err := controller.KeyFunc(rs)
		if err != nil {
			glog.Errorf("Couldn't get key for ReplicaSet %#v: %v", rs, err)
			return
		}
		rsc.expectations.CreationObserved(rsKey)
		rsc.enqueueReplicaSet(rs)
	}
}

// When a pod is updated, figure out what replica set/s manage it and wake them
// up. If the labels of the pod have changed we need to awaken both the old
// and new replica set. old and cur must be *api.Pod types.
func (rsc *ReplicaSetController) updatePod(old, cur interface{}) {
	if api.Semantic.DeepEqual(old, cur) {
		// A periodic relist will send update events for all known pods.
		return
	}
	// TODO: Write a unittest for this case
	curPod := cur.(*api.Pod)
	if curPod.DeletionTimestamp != nil {
		// when a pod is deleted gracefully it's deletion timestamp is first modified to reflect a grace period,
		// and after such time has passed, the kubelet actually deletes it from the store. We receive an update
		// for modification of the deletion timestamp and expect an ReplicaSet to create more replicas asap, not wait
		// until the kubelet actually deletes the pod. This is different from the Phase of a pod changing, because
		// a ReplicaSet never initiates a phase change, and so is never asleep waiting for the same.
		rsc.deletePod(curPod)
		return
	}
	if rs := rsc.getPodReplicaSet(curPod); rs != nil {
		rsc.enqueueReplicaSet(rs)
	}
	oldPod := old.(*api.Pod)
	// Only need to get the old replica set if the labels changed.
	if !reflect.DeepEqual(curPod.Labels, oldPod.Labels) {
		// If the old and new ReplicaSet are the same, the first one that syncs
		// will set expectations preventing any damage from the second.
		if oldRS := rsc.getPodReplicaSet(oldPod); oldRS != nil {
			rsc.enqueueReplicaSet(oldRS)
		}
	}
}

// When a pod is deleted, enqueue the replica set that manages the pod and update its expectations.
// obj could be an *api.Pod, or a DeletionFinalStateUnknown marker item.
func (rsc *ReplicaSetController) deletePod(obj interface{}) {
	pod, ok := obj.(*api.Pod)

	// When a delete is dropped, the relist will notice a pod in the store not
	// in the list, leading to the insertion of a tombstone object which contains
	// the deleted key/value. Note that this value might be stale. If the pod
	// changed labels the new ReplicaSet will not be woken up till the periodic resync.
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			glog.Errorf("Couldn't get object from tombstone %+v, could take up to %v before a replica set recreates a replica", obj, controller.ExpectationsTimeout)
			return
		}
		pod, ok = tombstone.Obj.(*api.Pod)
		if !ok {
			glog.Errorf("Tombstone contained object that is not a pod %+v, could take up to %v before replica set recreates a replica", obj, controller.ExpectationsTimeout)
			return
		}
	}
	if rs := rsc.getPodReplicaSet(pod); rs != nil {
		rsKey, err := controller.KeyFunc(rs)
		if err != nil {
			glog.Errorf("Couldn't get key for ReplicaSet %#v: %v", rs, err)
			return
		}
		rsc.expectations.DeletionObserved(rsKey)
		rsc.enqueueReplicaSet(rs)
	}
}

// obj could be an *extensions.ReplicaSet, or a DeletionFinalStateUnknown marker item.
func (rsc *ReplicaSetController) enqueueReplicaSet(obj interface{}) {
	key, err := controller.KeyFunc(obj)
	if err != nil {
		glog.Errorf("Couldn't get key for object %+v: %v", obj, err)
		return
	}

	// TODO: Handle overlapping replica sets better. Either disallow them at admission time or
	// deterministically avoid syncing replica sets that fight over pods. Currently, we only
	// ensure that the same replica set is synced for a given pod. When we periodically relist
	// all replica sets there will still be some replica instability. One way to handle this is
	// by querying the store for all replica sets that this replica set overlaps, as well as all
	// replica sets that overlap this ReplicaSet, and sorting them.
	rsc.queue.Add(key)
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (rsc *ReplicaSetController) worker() {
	for {
		func() {
			key, quit := rsc.queue.Get()
			if quit {
				return
			}
			defer rsc.queue.Done(key)
			err := rsc.syncHandler(key.(string))
			if err != nil {
				glog.Errorf("Error syncing ReplicaSet: %v", err)
			}
		}()
	}
}

// manageReplicas checks and updates replicas for the given ReplicaSet.
func (rsc *ReplicaSetController) manageReplicas(filteredPods []*api.Pod, rs *extensions.ReplicaSet) {
	diff := len(filteredPods) - rs.Spec.Replicas
	rsKey, err := controller.KeyFunc(rs)
	if err != nil {
		glog.Errorf("Couldn't get key for ReplicaSet %#v: %v", rs, err)
		return
	}
	if diff < 0 {
		diff *= -1
		if diff > rsc.burstReplicas {
			diff = rsc.burstReplicas
		}
		rsc.expectations.ExpectCreations(rsKey, diff)
		wait := sync.WaitGroup{}
		wait.Add(diff)
		glog.V(2).Infof("Too few %q/%q replicas, need %d, creating %d", rs.Namespace, rs.Name, rs.Spec.Replicas, diff)
		for i := 0; i < diff; i++ {
			go func() {
				defer wait.Done()
				if err := rsc.podControl.CreatePods(rs.Namespace, rs.Spec.Template, rs); err != nil {
					// Decrement the expected number of creates because the informer won't observe this pod
					glog.V(2).Infof("Failed creation, decrementing expectations for replica set %q/%q", rs.Namespace, rs.Name)
					rsc.expectations.CreationObserved(rsKey)
					utilruntime.HandleError(err)
				}
			}()
		}
		wait.Wait()
	} else if diff > 0 {
		if diff > rsc.burstReplicas {
			diff = rsc.burstReplicas
		}
		rsc.expectations.ExpectDeletions(rsKey, diff)
		glog.V(2).Infof("Too many %q/%q replicas, need %d, deleting %d", rs.Namespace, rs.Name, rs.Spec.Replicas, diff)
		// No need to sort pods if we are about to delete all of them
		if rs.Spec.Replicas != 0 {
			// Sort the pods in the order such that not-ready < ready, unscheduled
			// < scheduled, and pending < running. This ensures that we delete pods
			// in the earlier stages whenever possible.
			sort.Sort(controller.ActivePods(filteredPods))
		}

		wait := sync.WaitGroup{}
		wait.Add(diff)
		for i := 0; i < diff; i++ {
			go func(ix int) {
				defer wait.Done()
				if err := rsc.podControl.DeletePod(rs.Namespace, filteredPods[ix].Name, rs); err != nil {
					// Decrement the expected number of deletes because the informer won't observe this deletion
					glog.V(2).Infof("Failed deletion, decrementing expectations for replica set %q/%q", rs.Namespace, rs.Name)
					rsc.expectations.DeletionObserved(rsKey)
					utilruntime.HandleError(err)
				}
			}(i)
		}
		wait.Wait()
	}
}

// syncReplicaSet will sync the ReplicaSet with the given key if it has had its expectations fulfilled,
// meaning it did not expect to see any more of its pods created or deleted. This function is not meant to be
// invoked concurrently with the same key.
func (rsc *ReplicaSetController) syncReplicaSet(key string) error {
	startTime := time.Now()
	defer func() {
		glog.V(4).Infof("Finished syncing replica set %q (%v)", key, time.Now().Sub(startTime))
	}()

	obj, exists, err := rsc.rsStore.Store.GetByKey(key)
	if !exists {
		glog.Infof("ReplicaSet has been deleted %v", key)
		rsc.expectations.DeleteExpectations(key)
		return nil
	}
	if err != nil {
		glog.Infof("Unable to retrieve ReplicaSet %v from store: %v", key, err)
		rsc.queue.Add(key)
		return err
	}
	rs := *obj.(*extensions.ReplicaSet)
	if !rsc.podStoreSynced() {
		// Sleep so we give the pod reflector goroutine a chance to run.
		time.Sleep(PodStoreSyncedPollPeriod)
		glog.Infof("Waiting for pods controller to sync, requeuing ReplicaSet %v", rs.Name)
		rsc.enqueueReplicaSet(&rs)
		return nil
	}

	// Check the expectations of the ReplicaSet before counting active pods, otherwise a new pod can sneak
	// in and update the expectations after we've retrieved active pods from the store. If a new pod enters
	// the store after we've checked the expectation, the ReplicaSet sync is just deferred till the next
	// relist.
	rsKey, err := controller.KeyFunc(&rs)
	if err != nil {
		glog.Errorf("Couldn't get key for ReplicaSet %#v: %v", rs, err)
		return err
	}
	rsNeedsSync := rsc.expectations.SatisfiedExpectations(rsKey)
	selector, err := unversioned.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		glog.Errorf("Error converting pod selector to selector: %v", err)
		return err
	}
	podList, err := rsc.podStore.Pods(rs.Namespace).List(selector)
	if err != nil {
		glog.Errorf("Error getting pods for ReplicaSet %q: %v", key, err)
		rsc.queue.Add(key)
		return err
	}

	// TODO: Do this in a single pass, or use an index.
	filteredPods := controller.FilterActivePods(podList.Items)
	if rsNeedsSync {
		rsc.manageReplicas(filteredPods, &rs)
	}

	// Always updates status as pods come up or die.
	if err := updateReplicaCount(rsc.kubeClient.Extensions().ReplicaSets(rs.Namespace), rs, len(filteredPods)); err != nil {
		// Multiple things could lead to this update failing. Requeuing the replica set ensures
		// we retry with some fairness.
		glog.V(2).Infof("Failed to update replica count for controller %v/%v; requeuing; error: %v", rs.Namespace, rs.Name, err)
		rsc.enqueueReplicaSet(&rs)
	}
	return nil
}

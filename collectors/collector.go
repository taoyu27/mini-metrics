package collectors

import (
	"time"
	"net/http"
	
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/google/cadvisor/cache/memory"
	cmanager "github.com/google/cadvisor/manager"
	cadvisormetrics "github.com/google/cadvisor/container"
	//cinfo "github.com/google/cadvisor/info/v1"
	"github.com/google/cadvisor/utils/sysfs"
	
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	//api "k8s.io/kubernetes/pkg/apis/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	resourceclient "k8s.io/metrics/pkg/client/clientset_generated/clientset/typed/metrics/v1beta1"
	
	"sync"
)

var (	
	resyncPeriod = 10 * time.Minute
)

const statsCacheDuration = 2 * time.Minute
const maxHousekeepingInterval = 15 * time.Second
const defaultHousekeepingInterval = 10 * time.Second
const allowDynamicHousekeeping = true

const (
	statusBuilding = iota
	statusFailed
	statusRunning
	statusStopped
)

const (
	resourceMemory = iota
	resourceCPU
	resourceDisk
)

type StatusInfo struct {
	name string
	namespace string
	status int
}

type UtilizInfo struct {
	resource 	int
	namespace 	string
	podname   	string
	tenantid  	string
	servicename string
	value 		float64
}

type PodInfo struct {
	name    	string
	namespace	string
	tenantId	string
}

type ServiceCollector struct {
	fastSerivceStatus	[]*prometheus.GaugeVec
	fastResourceUtil    []*prometheus.GaugeVec
	pStore				podStore
	dStore				deploymentStore
	rStore      		replicasetStore
	statues             chan StatusInfo
	util				chan UtilizInfo
	done                chan struct{}
	mClient             *resourceclient.MetricsV1beta1Client
	mu                  sync.Mutex
	cManager        	cmanager.Manager                  
}

func RegisterServiceCollector(kubeClient kubernetes.Interface, metricsClient *resourceclient.MetricsV1beta1Client, namespace string, ch chan struct{}) {
	//collector containers by cadvisor
	sysFs := sysfs.NewRealSysFs()
	ignoreMetrics := cadvisormetrics.MetricSet{cadvisormetrics.NetworkTcpUsageMetrics: struct{}{}, cadvisormetrics.NetworkUdpUsageMetrics: struct{}{}}
	m, err := cmanager.New(memory.New(statsCacheDuration, nil), 
		sysFs, maxHousekeepingInterval, allowDynamicHousekeeping, ignoreMetrics, http.DefaultClient)
	if err != nil {
		glog.Errorf("cmanager.New failed %v", err)
		return 
	} else {
		glog.V(3).Infof("cmanager.New: %v", m)
		err = m.Start()
		if err != nil {
			glog.Errorf("cmanager.Start Failed: %v", err)
		}
	}
	
	podLister := registerPodCollector(kubeClient, namespace)
	dplLister := registerDeploymentCollector(kubeClient, namespace)
	replicaSetLister := registerReplicaSetCollector(kubeClient, namespace)
	sc := newServiceCollector(podLister, dplLister, replicaSetLister, m, metricsClient, ch)
	prometheus.Register(sc)
	
	// just test k8s-client
	//testNodeListUpdate(kubeClient)
	//testQuotaListUpdate(kubeClient, namespace)
	
	
	//TODO: need close goroutine such as signalKillHandle..
	go sc.waitStatus()
	go sc.waitUtilization()
}

func newServiceCollector(ps podStore, ds deploymentStore, rs replicasetStore, 
	m cmanager.Manager, metricsClient *resourceclient.MetricsV1beta1Client, ch chan struct{})*ServiceCollector{
	labels := make(prometheus.Labels)

	return &ServiceCollector{
		fastSerivceStatus: []*prometheus.GaugeVec{ 
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   "fast",
				Name:        "service_status_building",
				Help:        "TEST FOR SERVICE STATUS",
				ConstLabels: labels,
			},
			[]string{"service_name", "namespace"},
		),
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   "fast",
				Name:        "service_status_failed",
				Help:        "TEST FOR SERVICE STATUS",
				ConstLabels: labels,
			},
			[]string{"service_name", "namespace"},
		),
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   "fast",
				Name:        "service_status_runnning",
				Help:        "TEST FOR SERVICE STATUS",
				ConstLabels: labels,
			},
			[]string{"service_name", "namespace"},
		),
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   "fast",
				Name:        "service_status_stopped",
				Help:        "TEST FOR SERVICE STATUS",
				ConstLabels: labels,
			},
			[]string{"service_name", "namespace"},
		),
		},
		fastResourceUtil: []*prometheus.GaugeVec {
		prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace:   "fast",
				Name:        "pod_memory_utilization",
				Help:        "POD MEMORY UTILIZATION",
				ConstLabels: labels,
			},
			[]string{"pod_name", "namespace", "tenantId", "service_name"},
		),
		},
		pStore: ps,
		dStore: ds,
		rStore: rs,
		statues: make(chan StatusInfo),
		util: make(chan UtilizInfo),
		mClient: metricsClient,
		done:   ch,
		cManager:     m,
	}
}

func (s *ServiceCollector)calculateStatus(rs v1beta1.ReplicaSet, pods *[]v1.Pod){
	var sinfo = StatusInfo{
		name: rs.Name,
		namespace: rs.Namespace,
	}
	
	//Record Deployments/DaemonSets name as service name
	owners := rs.GetOwnerReferences()
	if len(owners) > 0 {
		if owners[0].Controller != nil {
			sinfo.name = owners[0].Name
		}
	}
	
	waitingReason := func(cs v1.ContainerStatus)string{
		if cs.State.Waiting == nil {
			return ""
		}
		return cs.State.Waiting.Reason
	}
	
	//TODO: move pod-ref-rs out
	hasErrorPod := func(rs v1beta1.ReplicaSet, pods *[]v1.Pod)bool{
		for _, pod := range *pods{
			ow := pod.GetOwnerReferences()
			if len(ow) > 0 {
				if ow[0].Name == rs.Name {
					for _, cs := range pod.Status.ContainerStatuses {
						reason := waitingReason(cs)
						if reason == "ImagePullBackOff" || reason == "ErrImagePull"  {
							return true
						}
					}
				}
			}
		}
		return false
	}
	  
	if rs.Status.AvailableReplicas == rs.Status.Replicas &&
	rs.Status.AvailableReplicas > 0{
		sinfo.status = statusRunning
	} else if rs.Status.ReadyReplicas < *rs.Spec.Replicas {
		if hasErrorPod(rs, pods) {
			sinfo.status = statusFailed
		} else {
			sinfo.status = statusBuilding
		}
	} else if *rs.Spec.Replicas == 0 {
		sinfo.status = statusStopped
	} else {
		sinfo.status = statusBuilding
	}
	s.statues<-sinfo
}

func (s *ServiceCollector)calculateSetValue(){
	replicasets, err := s.rStore.List()
	if err != nil {
		glog.Errorf("listing replicasets failed: %s", err)
	} 
	pods, err := s.pStore.List()
	if err != nil {
		glog.Errorf("listing pods failed: %s", err)
	} 
	for _, r := range replicasets {
		s.calculateStatus(r, &pods)
	}
}

func (s *ServiceCollector)waitStatus(){
	for {
		select {
			case recv := <-s.statues:
				s.mu.Lock()
				for k, status := range s.fastSerivceStatus {
					if k == recv.status {
						status.WithLabelValues(recv.name, recv.namespace).Set(1)
					} else {
						status.WithLabelValues(recv.name, recv.namespace).Set(0)
					}
				}
				s.mu.Unlock()
			case <-s.done:
				glog.V(3).Infof("Received SIGTERM, exiting gracefully..")
				return	
		}
	}
}

func (s *ServiceCollector)waitUtilization(){
	for {
		select {
			case recv := <-s.util:
				s.mu.Lock()
				for k, status := range s.fastResourceUtil {
					if k == recv.resource {
						status.WithLabelValues(recv.podname, recv.namespace, recv.tenantid, recv.servicename).Set(recv.value)
						break
					}
				}
				s.mu.Unlock()
			case <-s.done:
				glog.V(3).Infof("Received SIGTERM, exiting gracefully..")
				return	
		}
	}
}

func (s *ServiceCollector)collect()error{
	glog.V(3).Infof("Collect at %v\n", time.Now())
	res, err := s.getPodMetrics()
	if err != nil {
		return err
	}
	glog.V(3).Infof("PodMetricsInfo: %#v", res)
	
	//containers, err := s.cManager.SubcontainersInfo("/", &cinfo.ContainerInfoRequest{NumStats: 1})
	if err != nil {
		glog.Errorf("SubcontainersInfo Failed: %v", err)
		return err
	}
	pods, err := s.pStore.List()
	if err != nil {
		glog.Errorf("ListPods Failed: %v", err)
		return err
	}
	itemsLen := len(pods)
	requests := make(map[string]float64, itemsLen)
	//localMetrics := make(map[string]int64, itemsLen)
	if err != nil {
		glog.Errorf("listing pods failed: %s", err)
		return err
	} else {
		for _, pod := range pods {
			s.displayPod(pod)
			if pod.Status.Phase != v1.PodRunning || !IsPodReady(&pod) {
				glog.V(2).Infof("pod %s unready, skip.", pod.Name)
				continue
			}
			//podMetricsSum := s.podMetricsSum(pod, containers)
			podRequestSum := s.podRequestSum(pod)
			requests[pod.Name] = podRequestSum
			//localMetrics[pod.Name] = podMetricsSum
		}
	}
	glog.V(3).Infof("request: %v", requests)
	//glog.V(3).Infof("local_metrics: %v", localMetrics)
	
	utilization := make(map[string]float64, itemsLen)
	for podName, requestValue := range requests {
		if requestValue <= 0 {
			utilization[podName] = -1
		} else {
			utilization[podName] = res[podName] * 100 / requestValue
		}
	}
	glog.V(3).Infof("utilization: %v", utilization)
	maps := make(map[string]string, itemsLen)
	maps = s.getPodDeploymentMap(pods)
	s.sendUtilizations(utilization, pods, maps)
	
	deployments, err := s.dStore.List()
	if err != nil {
		glog.Errorf("listing deployment failed: %s", err)
	} else {
		for _, d := range deployments {
			s.displayDeployment(d)
		}
	}
	
	replicasets, err := s.rStore.List()
	if err != nil {
		glog.Errorf("listing replicasets failed: %s", err)
	} else {
		for _, r := range replicasets {
			s.displayReplicaSet(r)
		}
	}
	
	s.calculateSetValue()
	
	return nil
}

func (s *ServiceCollector) Describe(ch chan<- *prometheus.Desc) {
	glog.V(3).Infof("Describe at %v\n", time.Now())
	for _, metric := range s.collectorList() {
		metric.Describe(ch)
	}
}

func (s *ServiceCollector) Collect(ch chan<- prometheus.Metric) {
	if err := s.collect(); err != nil {
		glog.Errorf("failed collecting service metrics: %v", err)
	}

	s.mu.Lock()
	for _, metric := range s.collectorList() {
		metric.Collect(ch)
	}
	s.mu.Unlock()
}

func (s *ServiceCollector) collectorList() []prometheus.Collector {
	var cl []prometheus.Collector
	for _, metrics := range s.fastSerivceStatus {
		cl = append(cl, metrics)
	}
	for _, metrics := range s.fastResourceUtil {
		cl = append(cl, metrics)
	}
	return cl
}

func (s *ServiceCollector)sendUtilizations(util map[string]float64, pods []v1.Pod, podmap map[string]string){
	var uinfo = UtilizInfo{
		resource : resourceMemory,
	}
	for _, pod := range pods {
		uinfo.podname = pod.Name
		uinfo.namespace = pod.Namespace
		uinfo.tenantid = pod.Labels[FastTenantIdLabel]
		uinfo.servicename = podmap[uinfo.podname]
		value, ok := util[uinfo.podname]
		if !ok {
			uinfo.value = -1
		} else {
			uinfo.value = value
		}
		s.util<-uinfo
	}
}

func boolFloat64(b bool)float64{
	if b {
		return 1
	}
	return 0
}

// just test for k8s-client once list and update
func testNodeListUpdate(kubeClient kubernetes.Interface){
	nodes, err := kubeClient.Core().Nodes().List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("List nodes failed: %v", err)
		return
	}
	if len(nodes.Items) > 0 {
		node := nodes.Items[0]
		glog.V(3).Infof("Nodes: %#v", node)
		node.Annotations["checked"] = "true"
		_, err = kubeClient.Core().Nodes().Update(&node)
		if err != nil {
			glog.Errorf("Update node failed: %v", err)
		}
	}
}

func testQuotaListUpdate(kubeClient kubernetes.Interface, namespace string) {
	pods, err := kubeClient.Core().Pods(namespace).List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("List Pods failed: %v", err)
		return
	}
	requests := v1.ResourceList{}
	if len(pods.Items) > 0 {
		for _, pod := range pods.Items{
			for i := range pod.Spec.Containers {
				requests = QuotaAdd(requests, pod.Spec.Containers[i].Resources.Requests)
			}
			glog.V(5).Infof("[pod]-%s requst: %+v", pod.Name, requests)
		}
	}
	glog.V(3).Infof("requst memory: %#v", requests["memory"])
	glog.V(3).Infof("requst cpu: %#v", requests["cpu"])
	quotas, err := kubeClient.Core().ResourceQuotas(namespace).List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("List ResourceQuotas failed: %v", err)
		return
	}
	
	//newQuota := v1.ResourceQuota{}
	if len(quotas.Items) == 0 {
		return
	}
	for _, quota := range quotas.Items {
		glog.V(3).Infof("[quota]-%s use: %+v", quota.Name, quota.Status.Used)
		glog.V(3).Infof("[quota]-%s hard: %+v", quota.Name, quota.Status.Hard)
		glog.V(3).Infof("[quota]-%s hardMemory: %+v", quota.Name, quota.Spec.Hard["limits.memory"])
		glog.V(3).Infof("[quota]-%s usedMemory: %+v", quota.Name, quota.Status.Used["limits.memory"])
		glog.V(3).Infof("[quota]-%s usedMemoryValue: %#v", quota.Name, quota.Status.Used["limits.memory"])
		newQuota := quota
		newQuota.Status.Used["limits.memory"] = requests["memory"]
		 _, err = kubeClient.Core().ResourceQuotas(quota.Namespace).UpdateStatus(&newQuota)
		 if err != nil {
			 glog.Errorf("Update resource failed: %v", err)
		 }
		
		glog.V(3).Infof("[quota]NewQuota: %+v", newQuota)
	}
	
 
}

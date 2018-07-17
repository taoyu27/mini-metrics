package collectors

import (
	"strconv"
	
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/fields"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	
	//podutil "k8s.io/kubernetes/pkg/api/v1/pod"
)

type PodMetricsInfo map[string]float64

type PodLister func() ([]v1.Pod, error)
func (l PodLister) List() ([]v1.Pod, error) {
	return l()
}
type podStore interface {
	List() (pods []v1.Pod, err error)
}

func registerPodCollector(kubeClient kubernetes.Interface, namespace string)PodLister{
	client := kubeClient.CoreV1().RESTClient()
	glog.Infof("collect pod with %s", client.APIVersion())
	plw := cache.NewListWatchFromClient(client, "pods", namespace, fields.Everything())
	pinf := cache.NewSharedInformer(plw, &v1.Pod{}, resyncPeriod)
	go pinf.Run(context.Background().Done())
	
	podLister := PodLister(func() (pods []v1.Pod, err error) {
		for _, m := range pinf.GetStore().List() {
			pods = append(pods, *m.(*v1.Pod))
		}
		return pods, nil
	})
	return podLister
}

func (s *ServiceCollector)displayPod(pod v1.Pod){
	waitingReason := func(cs v1.ContainerStatus)string{
		if cs.State.Waiting == nil {
			return ""
		}
		return cs.State.Waiting.Reason
	}
	owners := pod.GetOwnerReferences()
	
	glog.V(3).Infof("*****************************")
	glog.V(5).Infof("[POD]%v", pod)
	glog.V(3).Infof("Pod[%s] || %s", pod.Name, pod.Namespace)
	glog.V(3).Infof("Node: %s", pod.Spec.NodeName)
	glog.V(3).Infof("Phase: %s", pod.Status.Phase)
	for _, cs := range pod.Status.ContainerStatuses {
		glog.V(3).Infof("Reason: %s", waitingReason(cs))
		glog.V(3).Infof("Image: %s", cs.Image)
	}
	if len(owners) > 0 {
		for _, owner := range owners{
			if owner.Controller != nil {
				glog.V(3).Infof("Owner: %s, %s, %s", 
					owner.Kind, owner.Name, strconv.FormatBool(*owner.Controller))
			} else {
				glog.V(3).Infof("Owner: %s, %s, %s", 
					owner.Kind, owner.Name, "false")
			}
		}
	}
	glog.V(3).Infof("PodIP: %s", pod.Status.PodIP)
	glog.V(3).Infof("QOSClass: %v", pod.Status.QOSClass)
	glog.V(3).Infof("*****************************")
}

func (s *ServiceCollector)podRequestSum(pod v1.Pod)float64 {
	var podSum float64
	for _, container := range pod.Spec.Containers {
		if containerRequest, ok := container.Resources.Requests["memory"]; ok {
			//podSum += containerRequest.MilliValue()
			podSum += float64(containerRequest.Value())
		} else {
			return -1
		}
	}
	return podSum
}

func (s *ServiceCollector)getPodMetrics()(PodMetricsInfo, error){
	metrics, err := s.mClient.PodMetricses(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("get PodMetricses failed %v", err)
		return nil, err
	}
	glog.V(5).Infof("metrics %#v", metrics)	
	res := make(PodMetricsInfo, len(metrics.Items))
	for _, m := range metrics.Items {
		podSum := float64(0)
		missing := len(m.Containers) == 0
		for _, c := range m.Containers {
			resValue, found := c.Usage[v1.ResourceName("memory")]
			if !found {
				missing = true
				glog.V(2).Infof("missing resource metric memory for container %s in pod %s/%s", c.Name, metav1.NamespaceAll, m.Name)
				break // containers loop
			}
			podSum += float64(resValue.Value())
		}
		if !missing {
			res[m.Name] = podSum
		}
	}
	return res, err
}

func (s *ServiceCollector)getPodDeploymentMap(pods []v1.Pod)map[string]string {
	getDpName := func(s string)string{
		var last int
		rs := []rune(s)
		for k, r := range rs {
			if r == '-' {
				last = k
			}
		}
		return string(rs[:last])
	}
	
	podmap := make(map[string]string, len(pods))
	for _, pod := range pods {
		owners := pod.GetOwnerReferences()
		if len(owners) > 0 && owners[0].Kind == "ReplicaSet" {
			rsName := owners[0].Name
			podmap[pod.Name] = getDpName(rsName)
			continue
		}
		podmap[pod.Name] = ""
	}
	return podmap
}

// IsPodReady returns true if a pod is ready; false otherwise.
func IsPodReady(pod *v1.Pod) bool {
	return IsPodReadyConditionTrue(pod.Status)
}

// IsPodReady retruns true if a pod is ready; false otherwise.
func IsPodReadyConditionTrue(status v1.PodStatus) bool {
	condition := GetPodReadyCondition(status)
	return condition != nil && condition.Status == v1.ConditionTrue
}

// Extracts the pod ready condition from the given status and returns that.
// Returns nil if the condition is not present.
func GetPodReadyCondition(status v1.PodStatus) *v1.PodCondition {
	_, condition := GetPodCondition(&status, v1.PodReady)
	return condition
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	for i := range status.Conditions {
		if status.Conditions[i].Type == conditionType {
			return i, &status.Conditions[i]
		}
	}
	return -1, nil
}
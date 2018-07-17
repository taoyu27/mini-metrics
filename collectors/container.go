package collectors

import (
	"github.com/golang/glog"
	
	"k8s.io/api/core/v1"
	cinfo "github.com/google/cadvisor/info/v1"
)

func (s *ServiceCollector)podMetricsSum(pod v1.Pod, containers []*cinfo.ContainerInfo)int64{
	var metricsSum int64
	for _, c := range containers {
		glog.V(3).Infof("containerRoot: %v, image: %v, pod_name: %v, name: %v, namespace: %v, memory: %d/%d MB", 
				c.Name, c.Spec.Image, c.Spec.Labels[KubernetesPodNameLabel], 
				c.Spec.Labels[KubernetesContainerNameLabel], c.Spec.Labels[KubernetesPodNamespaceLabel], 
				c.Stats[0].Memory.Usage/1024/1024, c.Stats[0].Memory.MaxUsage/1024/1024)
		if string(pod.UID) == c.Spec.Labels[KubernetesPodUIDLabel] {
			metricsSum += int64(c.Stats[0].Memory.Usage)
		}
	}
	return metricsSum
}
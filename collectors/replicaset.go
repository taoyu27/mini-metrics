package collectors

import (
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/fields"
)

type ReplicaSetLister func() ([]v1beta1.ReplicaSet, error)
func (l ReplicaSetLister) List() ([]v1beta1.ReplicaSet, error) {
	return l()
}
type replicasetStore interface {
	List() (replicasets []v1beta1.ReplicaSet, err error)
}

func registerReplicaSetCollector(kubeClient kubernetes.Interface, namespace string)ReplicaSetLister{
	client := kubeClient.ExtensionsV1beta1().RESTClient()
	glog.Infof("collect replicaset with %s", client.APIVersion())
	rslw := cache.NewListWatchFromClient(client, "replicasets", namespace, fields.Everything())
	rsinf := cache.NewSharedInformer(rslw, &v1beta1.ReplicaSet{}, resyncPeriod)
	replicaSetLister := ReplicaSetLister(func() (replicasets []v1beta1.ReplicaSet, err error) {
		for _, c := range rsinf.GetStore().List() {
			replicasets = append(replicasets, *(c.(*v1beta1.ReplicaSet)))
		}
		return replicasets, nil
	})
	go rsinf.Run(context.Background().Done())
	
	return replicaSetLister
}

func (s *ServiceCollector)displayReplicaSet(rs v1beta1.ReplicaSet){
	glog.V(3).Infof("*****************************")
	glog.V(5).Infof("[ReplicaSet]%v", rs)
	glog.V(3).Infof("ReplicaSet[%s] || %s", rs.Name, rs.Namespace)
	glog.V(3).Infof("Replicas: %d", rs.Status.Replicas)
	glog.V(3).Infof("ReadyReplicas: %d", rs.Status.ReadyReplicas)
	glog.V(3).Infof("AvailableReplicas: %d", rs.Status.AvailableReplicas)
	glog.V(5).Infof("Conditions: %#v", rs.Status.Conditions)
	glog.V(3).Infof("*****************************")
}
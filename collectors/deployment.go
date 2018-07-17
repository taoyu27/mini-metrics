package collectors

import (
	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/apimachinery/pkg/fields"
)

type DeploymentLister func() ([]v1beta1.Deployment, error)
func (l DeploymentLister) List() ([]v1beta1.Deployment, error) {
	return l()
}
type deploymentStore interface {
	List() (deployments []v1beta1.Deployment, err error)
}

func registerDeploymentCollector(kubeClient kubernetes.Interface, namespace string)DeploymentLister{
	client := kubeClient.ExtensionsV1beta1().RESTClient()
	glog.Infof("collect deployment with %s", client.APIVersion())
	dlw := cache.NewListWatchFromClient(client, "deployments", namespace, fields.Everything())
	dinf := cache.NewSharedInformer(dlw, &v1beta1.Deployment{}, resyncPeriod)
	dplLister := DeploymentLister(func() (deployments []v1beta1.Deployment, err error) {
		for _, c := range dinf.GetStore().List() {
			deployments = append(deployments, *(c.(*v1beta1.Deployment)))
		}
		return deployments, nil
	})
	go dinf.Run(context.Background().Done())
	
	//just test for informer handlers
	dinf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(o interface{}) {
			glog.V(5).Infof("catch AddFunc %v", o)
		},
		DeleteFunc: func(o interface{}) {
			glog.V(5).Infof("catch DeleteFunc %v", o)
		},
		UpdateFunc: func(_, o interface{}) {
			glog.V(5).Infof("catch UpdateFunc %v", o)
		},
	})	
	
	return dplLister
}

func (s *ServiceCollector)displayDeployment(dl v1beta1.Deployment){
	glog.V(3).Infof("*****************************")
	glog.V(5).Infof("[DEPLOYMENT]%v", dl)
	glog.V(3).Infof("Deployment[%s] || %s", dl.Name, dl.Namespace)
	glog.V(3).Infof("Replicas: %d", *dl.Spec.Replicas)
	glog.V(3).Infof("ReadyReplicas: %d", dl.Status.ReadyReplicas)
	glog.V(3).Infof("AvailableReplicas: %d", dl.Status.AvailableReplicas)
	glog.V(3).Infof("UnavailableReplicas: %d", dl.Status.UnavailableReplicas)
	glog.V(5).Infof("PodTemplate: %#v", dl.Spec.Template)
	glog.V(3).Infof("*****************************")
}
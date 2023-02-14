package controller

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	kubeovnv1 "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/kubeovn/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/kubeovn/kube-ovn/pkg/util"
)

func (c *Controller) enqueueAddVpc(obj interface{}) {

	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	klog.V(3).Infof("enqueue add vpc %s", key)
	vpc := obj.(*kubeovnv1.Vpc)
	if _, ok := vpc.Labels[util.VpcExternalLabel]; !ok {
		c.addOrUpdateVpcQueue.Add(key)
	}
}

func (c *Controller) enqueueUpdateVpc(old, new interface{}) {
	oldVpc := old.(*kubeovnv1.Vpc)
	newVpc := new.(*kubeovnv1.Vpc)

	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(new); err != nil {
		utilruntime.HandleError(err)
		return
	}

	_, oldOk := oldVpc.Labels[util.VpcExternalLabel]
	_, newOk := newVpc.Labels[util.VpcExternalLabel]
	if oldOk || newOk {
		return
	}

	if !newVpc.DeletionTimestamp.IsZero() ||
		!reflect.DeepEqual(oldVpc.Spec.Namespaces, newVpc.Spec.Namespaces) ||
		!reflect.DeepEqual(oldVpc.Spec.StaticRoutes, newVpc.Spec.StaticRoutes) ||
		!reflect.DeepEqual(oldVpc.Spec.PolicyRoutes, newVpc.Spec.PolicyRoutes) ||
		!reflect.DeepEqual(oldVpc.Spec.VpcPeerings, newVpc.Spec.VpcPeerings) ||
		!reflect.DeepEqual(oldVpc.Annotations, newVpc.Annotations) {
		klog.V(3).Infof("enqueue update vpc %s", key)
		c.addOrUpdateVpcQueue.Add(key)
	}
}

func (c *Controller) enqueueDelVpc(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	vpc := obj.(*kubeovnv1.Vpc)
	_, ok := vpc.Labels[util.VpcExternalLabel]
	if !vpc.Status.Default || !ok {
		klog.V(3).Infof("enqueue delete vpc %s", key)
		c.delVpcQueue.Add(obj)
	}
}

func (c *Controller) runAddVpcWorker() {
	for c.processNextAddVpcWorkItem() {
	}
}

func (c *Controller) runUpdateVpcStatusWorker() {
	for c.processNextUpdateStatusVpcWorkItem() {
	}
}

func (c *Controller) runDelVpcWorker() {
	for c.processNextDeleteVpcWorkItem() {
	}
}

func (c *Controller) handleDelVpc(vpc *kubeovnv1.Vpc) error {
	if err := c.deleteVpcLb(vpc); err != nil {
		return err
	}

	err := c.deleteVpcRouter(vpc.Status.Router)
	if err != nil {
		return err
	}

	if err := c.handleDelVpcExternal(vpc.Name); err != nil {
		klog.Errorf("failed to delete external connection for vpc %s, error %v", vpc.Name, err)
		return err
	}
	return nil
}

func (c *Controller) handleUpdateVpcStatus(key string) error {
	cachedVpc, err := c.vpcsLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	vpc := cachedVpc.DeepCopy()

	subnets, defaultSubnet, err := c.getVpcSubnets(vpc)
	if err != nil {
		return err
	}

	change := false
	if vpc.Status.DefaultLogicalSwitch != defaultSubnet {
		change = true
	}

	vpc.Status.DefaultLogicalSwitch = defaultSubnet
	vpc.Status.Subnets = subnets
	bytes, err := vpc.Status.Bytes()
	if err != nil {
		return err
	}

	vpc, err = c.config.KubeOvnClient.KubeovnV1().Vpcs().Patch(context.Background(), vpc.Name, types.MergePatchType, bytes, metav1.PatchOptions{}, "status")
	if err != nil {
		return err
	}
	if change {
		for _, ns := range vpc.Spec.Namespaces {
			c.addNamespaceQueue.Add(ns)
		}
	}

	natGws, err := c.vpcNatGatewayLister.List(labels.Everything())
	if err != nil {
		return err
	}
	for _, gw := range natGws {
		if key == gw.Spec.Vpc {
			c.updateVpcSubnetQueue.Add(gw.Name)
		}
	}
	return nil
}

type VpcLoadBalancer struct {
	TcpLoadBalancer     string
	TcpSessLoadBalancer string
	UdpLoadBalancer     string
	UdpSessLoadBalancer string
}

func (c *Controller) GenVpcLoadBalancer(vpcKey string) *VpcLoadBalancer {
	if vpcKey == util.DefaultVpc || vpcKey == "" {
		return &VpcLoadBalancer{
			TcpLoadBalancer:     c.config.ClusterTcpLoadBalancer,
			TcpSessLoadBalancer: c.config.ClusterTcpSessionLoadBalancer,
			UdpLoadBalancer:     c.config.ClusterUdpLoadBalancer,
			UdpSessLoadBalancer: c.config.ClusterUdpSessionLoadBalancer,
		}
	} else {
		return &VpcLoadBalancer{
			TcpLoadBalancer:     fmt.Sprintf("vpc-%s-tcp-load", vpcKey),
			TcpSessLoadBalancer: fmt.Sprintf("vpc-%s-tcp-sess-load", vpcKey),
			UdpLoadBalancer:     fmt.Sprintf("vpc-%s-udp-load", vpcKey),
			UdpSessLoadBalancer: fmt.Sprintf("vpc-%s-udp-sess-load", vpcKey),
		}
	}
}

func (c *Controller) addLoadBalancer(vpcName string) (*VpcLoadBalancer, error) {
	vpcLb := c.GenVpcLoadBalancer(vpcName)

	if err := c.ovnClient.CreateLoadBalancer(vpcLb.TcpLoadBalancer, util.ProtocolTCP, ""); err != nil {
		klog.Errorf("create vpc %s tcp load balancer: %v", vpcName, err)
		return nil, err
	}

	if err := c.ovnClient.CreateLoadBalancer(vpcLb.UdpLoadBalancer, util.ProtocolUDP, ""); err != nil {
		klog.Errorf("create vpc %s udp load balancer: %v", vpcName, err)
		return nil, err
	}

	if err := c.ovnClient.CreateLoadBalancer(vpcLb.TcpSessLoadBalancer, util.ProtocolTCP, "ip_src"); err != nil {
		klog.Errorf("create vpc %s tcp session load balancer: %v", vpcName, err)
		return nil, err
	}

	if err := c.ovnClient.CreateLoadBalancer(vpcLb.UdpSessLoadBalancer, util.ProtocolUDP, "ip_src"); err != nil {
		klog.Errorf("create vpc %s udp session load balancer: %v", vpcName, err)
		return nil, err
	}

	if err := c.ovnLegacyClient.SetLoadBalancerAffinityTimeout(vpcLb.TcpSessLoadBalancer, util.DefaultServiceSessionStickinessTimeout); err != nil {
		klog.Errorf("failed to set service session stickiness timeout of cluster tcp session load balancer: %v", err)
		return nil, err
	}

	if err := c.ovnLegacyClient.SetLoadBalancerAffinityTimeout(vpcLb.UdpSessLoadBalancer, util.DefaultServiceSessionStickinessTimeout); err != nil {
		klog.Errorf("failed to set service session stickiness timeout of cluster udp session load balancer: %v", err)
		return nil, err
	}

	return vpcLb, nil
}

func (c *Controller) GetStaticRoutes(lrName string) (routes []*kubeovnv1.StaticRoute, err error) {
	output, err := c.ovnClient.ListLogicalRouterStaticRoutes(map[string]string{logicalRouterKey: lrName})
	if err != nil {
		return nil, fmt.Errorf("list logical router routes: %v", err)
	}

	for _, r := range output {
		routes = append(routes, &kubeovnv1.StaticRoute{
			Policy:    kubeovnv1.RoutePolicy(*r.Policy),
			CIDR:      r.IPPrefix,
			NextHopIP: r.Nexthop,
		})
	}

	return routes, nil
}

func (c *Controller) GetPolicyRoutes(lrName string) (routes []*kubeovnv1.PolicyRoute, err error) {
	// list all policies from router
	output, err := c.ovnClient.ListLogicalRouterPolicies(-1, map[string]string{logicalRouterKey: lrName})
	if err != nil {
		return nil, fmt.Errorf("list policy routes: %v", err)
	}

	for _, r := range output {
		routes = append(routes, &kubeovnv1.PolicyRoute{
			Priority:  int32(r.Priority),
			Match:     r.Match,
			Action:    kubeovnv1.PolicyRouteAction(r.Action),
			NextHopIP: r.Nexthops[0],
		})
	}

	return routes, nil
}

func (c *Controller) handleAddOrUpdateVpc(key string) error {
	// get latest vpc info
	cachedVpc, err := c.config.KubeOvnClient.KubeovnV1().Vpcs().Get(context.Background(), key, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	vpc := cachedVpc.DeepCopy()

	if err = formatVpc(vpc, c); err != nil {
		klog.Errorf("failed to format vpc: %v", err)
		return err
	}
	if err = c.createVpcRouter(key); err != nil {
		return err
	}

	var newPeers []string
	for _, peering := range vpc.Spec.VpcPeerings {
		if err = util.CheckCidrs(peering.LocalConnectIP); err != nil {
			klog.Errorf("invalid cidr %s", peering.LocalConnectIP)
			return err
		}
		newPeers = append(newPeers, peering.RemoteVpc)

		if err := c.ovnClient.CreatePeerRouterPort(vpc.Name, peering.RemoteVpc, peering.LocalConnectIP); err != nil {
			klog.Errorf("failed to create peer router port for vpc %s, %v", vpc.Name, err)
			return err
		}
	}

	for _, oldPeer := range vpc.Status.VpcPeerings {
		if !util.ContainsString(newPeers, oldPeer) {
			lrpName := fmt.Sprintf("%s-%s", vpc.Name, oldPeer)
			klog.Infof("delete logical router port %s", lrpName)
			if err = c.ovnClient.DeleteLogicalRouterPort(lrpName); err != nil {
				klog.Errorf("failed to delete peer router port for vpc %s, %v", vpc.Name, err)
				return err
			}
		}
	}

	// handle static route
	existRoute, err := c.GetStaticRoutes(vpc.Name)
	if err != nil {
		klog.Errorf("get vpc %s static routes: %v", vpc.Name, err)
		return err
	}

	targetRoutes := vpc.Spec.StaticRoutes
	if vpc.Name == c.config.ClusterRouter {
		joinSubnet, err := c.subnetsLister.Get(c.config.NodeSwitch)
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				klog.Error("failed to get node switch subnet %s: %v", c.config.NodeSwitch)
				return err
			}
		}
		gatewayV4, gatewayV6 := util.SplitStringIP(joinSubnet.Spec.Gateway)
		if gatewayV4 != "" {
			targetRoutes = append(targetRoutes, &kubeovnv1.StaticRoute{
				Policy:    kubeovnv1.PolicyDst,
				CIDR:      "0.0.0.0/0",
				NextHopIP: gatewayV4,
			})
		}
		if gatewayV6 != "" {
			targetRoutes = append(targetRoutes, &kubeovnv1.StaticRoute{
				Policy:    kubeovnv1.PolicyDst,
				CIDR:      "::/0",
				NextHopIP: gatewayV6,
			})
		}

		if c.config.EnableEipSnat {
			cm, err := c.configMapsLister.ConfigMaps(c.config.ExternalGatewayConfigNS).Get(util.ExternalGatewayConfig)
			if err == nil {
				nextHop := cm.Data["external-gw-addr"]
				if nextHop == "" {
					klog.Errorf("no available gateway nic address")
					return fmt.Errorf("no available gateway nic address")
				}
				if strings.Contains(nextHop, "/") {
					nextHop = strings.Split(nextHop, "/")[0]
				}

				// list all nats from router
				nats, err := c.ovnClient.ListNats("", "", map[string]string{logicalRouterKey: vpc.Name})
				if err != nil {
					klog.Errorf("failed to get nat for vpc %s: %v", vpc.Name, err)
					return err
				}

				for _, nat := range nats {
					if nat.LogicalIP != "" {
						targetRoutes = append(targetRoutes, &kubeovnv1.StaticRoute{
							Policy:    kubeovnv1.PolicySrc,
							CIDR:      nat.LogicalIP,
							NextHopIP: nextHop,
						})
					}
				}
			}
		}
	}

	routeNeedDel, routeNeedAdd, err := diffStaticRoute(existRoute, targetRoutes)
	if err != nil {
		klog.Errorf("failed to diff vpc %s static route, %v", vpc.Name, err)
		return err
	}
	for _, item := range routeNeedDel {
		if err = c.ovnClient.DeleteLogicalRouterStaticRoute(vpc.Name, string(item.Policy), item.CIDR, item.NextHopIP, ""); err != nil {
			klog.Errorf("del vpc %s static route failed, %v", vpc.Name, err)
			return err
		}
	}

	for _, item := range routeNeedAdd {
		if err = c.ovnClient.AddLogicalRouterStaticRoute(vpc.Name, convertPolicy(item.Policy), item.CIDR, item.NextHopIP, util.NormalRouteType); err != nil {
			klog.Errorf("add static route to vpc %s failed, %v", vpc.Name, err)
			return err
		}
	}

	if vpc.Name != util.DefaultVpc && vpc.Spec.PolicyRoutes == nil {
		// do not clean default vpc policy routes
		if err = c.ovnClient.ClearLogicalRouterPolicy(vpc.Name); err != nil {
			klog.Errorf("clean all vpc %s policy route failed, %v", vpc.Name, err)
			return err
		}
	}

	if vpc.Spec.PolicyRoutes != nil {
		// diff update vpc policy route
		existPolicyRoute, err := c.GetPolicyRoutes(vpc.Name)
		if err != nil {
			klog.Errorf("get vpc %s policy routes: %v", vpc.Name, err)
			return err
		}

		policyRouteNeedDel, policyRouteNeedAdd, err := diffPolicyRoute(existPolicyRoute, vpc.Spec.PolicyRoutes)
		if err != nil {
			klog.Errorf("failed to diff vpc %s policy route, %v", vpc.Name, err)
			return err
		}
		for _, item := range policyRouteNeedDel {
			klog.Infof("delete policy route for router: %s, priority: %d, match %s", vpc.Name, item.Priority, item.Match)

			if err = c.ovnClient.DeleteLogicalRouterPolicy(vpc.Name, int(item.Priority), item.Match); err != nil {
				klog.Errorf("del vpc %s policy route failed, %v", vpc.Name, err)
				return err
			}
		}
		for _, item := range policyRouteNeedAdd {
			externalIDs := map[string]string{"vendor": util.CniTypeName}
			klog.Infof("add policy route for router: %s, match %s, action %s, nexthop %s, extrenalID %v", c.config.ClusterRouter, item.Match, string(item.Action), item.NextHopIP, externalIDs)

			if err = c.ovnClient.AddLogicalRouterPolicy(vpc.Name, int(item.Priority), item.Match, string(item.Action), []string{item.NextHopIP}, externalIDs); err != nil {
				klog.Errorf("add policy route to vpc %s failed, %v", vpc.Name, err)
				return err
			}
		}
	}

	vpc.Status.Router = key
	vpc.Status.Standby = true
	vpc.Status.VpcPeerings = newPeers
	if c.config.EnableLb {
		vpcLb, err := c.addLoadBalancer(key)
		if err != nil {
			return err
		}
		vpc.Status.TcpLoadBalancer = vpcLb.TcpLoadBalancer
		vpc.Status.TcpSessionLoadBalancer = vpcLb.TcpSessLoadBalancer
		vpc.Status.UdpLoadBalancer = vpcLb.UdpLoadBalancer
		vpc.Status.UdpSessionLoadBalancer = vpcLb.UdpSessLoadBalancer
	}
	bytes, err := vpc.Status.Bytes()
	if err != nil {
		return err
	}
	vpc, err = c.config.KubeOvnClient.KubeovnV1().Vpcs().Patch(context.Background(), vpc.Name, types.MergePatchType, bytes, metav1.PatchOptions{}, "status")
	if err != nil {
		return err
	}

	if len(vpc.Annotations) != 0 && strings.ToLower(vpc.Annotations[util.VpcLbAnnotation]) == "on" {
		if err = c.createVpcLb(vpc); err != nil {
			return err
		}
	} else if err = c.deleteVpcLb(vpc); err != nil {
		return err
	}

	subnets, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		return err
	}

	for _, subnet := range subnets {
		if subnet.Spec.Vpc == key {
			c.addOrUpdateSubnetQueue.Add(subnet.Name)
		}
	}

	if cachedVpc.Spec.EnableExternal && !cachedVpc.Status.EnableExternal {
		// connecte vpc to external
		if err := c.handleAddVpcExternal(key); err != nil {
			klog.Errorf("failed to add external connection for vpc %s, error %v", key, err)
			return err
		}
	}

	if !cachedVpc.Spec.EnableExternal && cachedVpc.Status.EnableExternal {
		// disconnect vpc to external
		if err := c.handleDelVpcExternal(key); err != nil {
			klog.Errorf("failed to delete external connection for vpc %s, error %v", key, err)
			return err
		}
	}

	return nil
}

func diffPolicyRoute(exist []*kubeovnv1.PolicyRoute, target []*kubeovnv1.PolicyRoute) (routeNeedDel []*kubeovnv1.PolicyRoute, routeNeedAdd []*kubeovnv1.PolicyRoute, err error) {
	existRouteMap := make(map[string]*kubeovnv1.PolicyRoute, len(exist))
	for _, item := range exist {
		existRouteMap[getPolicyRouteItemKey(item)] = item
	}

	for _, item := range target {
		key := getPolicyRouteItemKey(item)
		if _, ok := existRouteMap[key]; ok {
			delete(existRouteMap, key)
		} else {
			routeNeedAdd = append(routeNeedAdd, item)
		}
	}
	for _, item := range existRouteMap {
		routeNeedDel = append(routeNeedDel, item)
	}
	return routeNeedDel, routeNeedAdd, nil
}

func getPolicyRouteItemKey(item *kubeovnv1.PolicyRoute) (key string) {
	return fmt.Sprintf("%d:%s:%s:%s", item.Priority, item.Match, item.Action, item.NextHopIP)
}

func diffStaticRoute(exist []*kubeovnv1.StaticRoute, target []*kubeovnv1.StaticRoute) (routeNeedDel []*kubeovnv1.StaticRoute, routeNeedAdd []*kubeovnv1.StaticRoute, err error) {
	existRouteMap := make(map[string]*kubeovnv1.StaticRoute, len(exist))
	for _, item := range exist {
		existRouteMap[getStaticRouteItemKey(item)] = item
	}

	for _, item := range target {
		key := getStaticRouteItemKey(item)
		if _, ok := existRouteMap[key]; ok {
			delete(existRouteMap, key)
		} else {
			routeNeedAdd = append(routeNeedAdd, item)
		}
	}
	for _, item := range existRouteMap {
		routeNeedDel = append(routeNeedDel, item)
	}
	return
}

func getStaticRouteItemKey(item *kubeovnv1.StaticRoute) (key string) {
	if item.Policy == kubeovnv1.PolicyDst {
		return fmt.Sprintf("dst:%s=>%s", item.CIDR, item.NextHopIP)
	} else {
		return fmt.Sprintf("src:%s=>%s", item.CIDR, item.NextHopIP)
	}
}

func formatVpc(vpc *kubeovnv1.Vpc, c *Controller) error {
	var changed bool
	for _, item := range vpc.Spec.StaticRoutes {
		// check policy
		if item.Policy == "" {
			item.Policy = kubeovnv1.PolicyDst
			changed = true
		}
		if item.Policy != kubeovnv1.PolicyDst && item.Policy != kubeovnv1.PolicySrc {
			return fmt.Errorf("unknown policy type: %s", item.Policy)
		}
		// check cidr
		if strings.Contains(item.CIDR, "/") {
			if _, _, err := net.ParseCIDR(item.CIDR); err != nil {
				return fmt.Errorf("invalid cidr %s: %w", item.CIDR, err)
			}
		} else if ip := net.ParseIP(item.CIDR); ip == nil {
			return fmt.Errorf("invalid IP %s", item.CIDR)
		}
		// check next hop ip
		if ip := net.ParseIP(item.NextHopIP); ip == nil {
			return fmt.Errorf("invalid next hop IP %s", item.NextHopIP)
		}
	}

	for _, route := range vpc.Spec.PolicyRoutes {
		if route.Action != kubeovnv1.PolicyRouteActionReroute {
			if route.NextHopIP != "" {
				route.NextHopIP = ""
				changed = true
			}
		} else {
			if ip := net.ParseIP(route.NextHopIP); ip == nil {
				return fmt.Errorf("bad next hop ip: %s", route.NextHopIP)
			}
		}
	}

	if changed {
		if _, err := c.config.KubeOvnClient.KubeovnV1().Vpcs().Update(context.Background(), vpc, metav1.UpdateOptions{}); err != nil {
			klog.Errorf("failed to update vpc %s: %v", vpc.Name, err)
			return err
		}
	}

	return nil
}

func convertPolicy(origin kubeovnv1.RoutePolicy) string {
	if origin == kubeovnv1.PolicyDst {
		return ovnnb.LogicalRouterStaticRoutePolicyDstIP
	} else {
		return ovnnb.LogicalRouterStaticRoutePolicySrcIP
	}
}

func (c *Controller) processNextUpdateStatusVpcWorkItem() bool {
	obj, shutdown := c.updateVpcStatusQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.updateVpcStatusQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.updateVpcStatusQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleUpdateVpcStatus(key); err != nil {
			c.updateVpcStatusQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.updateVpcStatusQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *Controller) processNextAddVpcWorkItem() bool {
	obj, shutdown := c.addOrUpdateVpcQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.addOrUpdateVpcQueue.Done(obj)
		var key string
		var ok bool
		if key, ok = obj.(string); !ok {
			c.addOrUpdateVpcQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleAddOrUpdateVpc(key); err != nil {
			// c.addOrUpdateVpcQueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		c.addOrUpdateVpcQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		c.addOrUpdateVpcQueue.AddRateLimited(obj)
		return true
	}
	return true
}

func (c *Controller) processNextDeleteVpcWorkItem() bool {
	obj, shutdown := c.delVpcQueue.Get()
	if shutdown {
		return false
	}

	err := func(obj interface{}) error {
		defer c.delVpcQueue.Done(obj)
		var vpc *kubeovnv1.Vpc
		var ok bool
		if vpc, ok = obj.(*kubeovnv1.Vpc); !ok {
			c.delVpcQueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		if err := c.handleDelVpc(vpc); err != nil {
			c.delVpcQueue.AddRateLimited(obj)
			return fmt.Errorf("error syncing '%s': %s, requeuing", vpc.Name, err.Error())
		}
		c.delVpcQueue.Forget(obj)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}
	return true
}

func (c *Controller) getVpcSubnets(vpc *kubeovnv1.Vpc) (subnets []string, defaultSubnet string, err error) {
	subnets = []string{}
	allSubnets, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		return nil, "", err
	}

	for _, subnet := range allSubnets {
		if subnet.Spec.Vpc != vpc.Name || !subnet.DeletionTimestamp.IsZero() || !isOvnSubnet(subnet) {
			continue
		}

		subnets = append(subnets, subnet.Name)
		if subnet.Spec.Default {
			defaultSubnet = subnet.Name
		}
	}
	return
}

// createVpcRouter create router to connect logical switches in vpc
func (c *Controller) createVpcRouter(lr string) error {
	return c.ovnClient.CreateLogicalRouter(lr)
}

// deleteVpcRouter delete router to connect logical switches in vpc
func (c *Controller) deleteVpcRouter(lr string) error {
	return c.ovnClient.DeleteLogicalRouter(lr)
}

func (c *Controller) handleAddVpcExternal(key string) error {
	cachedSubnet, err := c.subnetsLister.Get(c.config.ExternalGatewaySwitch)
	if err != nil {
		return err
	}
	lrpEipName := fmt.Sprintf("%s-%s", key, c.config.ExternalGatewaySwitch)
	cachedEip, err := c.ovnEipsLister.Get(lrpEipName)
	var needCreateEip bool
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		needCreateEip = true
	}
	var v4ip, v6ip, mac string
	klog.V(3).Infof("create vpc lrp eip %s", lrpEipName)
	if needCreateEip {
		if v4ip, v6ip, mac, err = c.acquireIpAddress(c.config.ExternalGatewaySwitch, lrpEipName, lrpEipName); err != nil {
			klog.Errorf("failed to acquire ip address for lrp eip %s, %v", lrpEipName, err)
			return err
		}
		if err := c.createOrUpdateCrdOvnEip(lrpEipName, c.config.ExternalGatewaySwitch, v4ip, v6ip, mac, util.LrpUsingEip); err != nil {
			klog.Errorf("failed to create ovn eip for lrp %s: %v", lrpEipName, err)
			return err
		}
	} else {
		v4ip = cachedEip.Spec.V4Ip
		mac = cachedEip.Spec.MacAddress
	}
	if v4ip == "" || mac == "" {
		return fmt.Errorf("lrp '%s' ip or mac should not be empty", lrpEipName)
	}
	if err = c.patchOvnEipStatus(lrpEipName); err != nil {
		return err
	}
	// init lrp gw chassis group
	cm, err := c.configMapsLister.ConfigMaps(c.config.ExternalGatewayConfigNS).Get(util.ExternalGatewayConfig)
	if err != nil && !k8serrors.IsNotFound(err) {
		klog.Errorf("failed to get ovn-external-gw-config, %v", err)
		return err
	}
	chassises, err := c.getGatewayChassis(cm.Data)
	if err != nil {
		klog.Errorf("failed to get gateway chassis, %v", err)
		return err
	}
	v4ipCidr := util.GetIpAddrWithMask(v4ip, cachedSubnet.Spec.CIDRBlock)
	lspName := fmt.Sprintf("%s-%s", c.config.ExternalGatewaySwitch, key)
	lrpName := fmt.Sprintf("%s-%s", key, c.config.ExternalGatewaySwitch)

	if err := c.ovnClient.CreateLogicalPatchPort(c.config.ExternalGatewaySwitch, key, lspName, lrpName, v4ipCidr, mac, chassises...); err != nil {
		klog.Errorf("failed to connect router '%s' to external: %v", key, err)
		return err
	}

	cachedVpc, err := c.vpcsLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	vpc := cachedVpc.DeepCopy()
	vpc.Status.EnableExternal = cachedVpc.Spec.EnableExternal
	bytes, err := vpc.Status.Bytes()
	if err != nil {
		return err
	}
	if _, err = c.config.KubeOvnClient.KubeovnV1().Vpcs().Patch(context.Background(),
		vpc.Name, types.MergePatchType, bytes, metav1.PatchOptions{}, "status"); err != nil {
		return err
	}
	cachedEip, err = c.ovnEipsLister.Get(lrpEipName)
	if err != nil {
		return err
	}
	if err = c.handleAddOvnEipFinalizer(cachedEip); err != nil {
		klog.Errorf("failed to add finalizer for ovn eip, %v", err)
		return err
	}
	return nil
}

func (c *Controller) handleDelVpcExternal(key string) error {
	cachedVpc, err := c.vpcsLister.Get(key)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	lspName := fmt.Sprintf("%s-%s", c.config.ExternalGatewaySwitch, key)
	lrpName := fmt.Sprintf("%s-%s", key, c.config.ExternalGatewaySwitch)
	klog.V(3).Infof("delete vpc lrp %s", lrpName)

	if err := c.ovnClient.RemoveLogicalPatchPort(lspName, lrpName); err != nil {
		klog.Errorf("failed to disconnect router '%s' to external, %v", key, err)
		return err
	}

	vpc := cachedVpc.DeepCopy()
	vpc.Status.EnableExternal = cachedVpc.Spec.EnableExternal
	bytes, err := vpc.Status.Bytes()
	if err != nil {
		return err
	}
	if _, err = c.config.KubeOvnClient.KubeovnV1().Vpcs().Patch(context.Background(),
		vpc.Name, types.MergePatchType, bytes, metav1.PatchOptions{}, "status"); err != nil {
		return err
	}
	cachedEip, err := c.ovnEipsLister.Get(lrpName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err = c.handleDelOvnEipFinalizer(cachedEip); err != nil {
		klog.Errorf("failed to del finalizer for ovn eip, %v", err)
		return err
	}
	if err = c.config.KubeOvnClient.KubeovnV1().OvnEips().Delete(context.Background(), lrpName, metav1.DeleteOptions{}); err != nil {
		if !k8serrors.IsNotFound(err) {
			klog.Errorf("failed to delete ovn eip %s, %v", lrpName, err)
			return err
		}
	}
	return nil
}

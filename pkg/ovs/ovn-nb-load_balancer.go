package ovs

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	ovsclient "github.com/kubeovn/kube-ovn/pkg/ovsdb/client"
	"github.com/kubeovn/kube-ovn/pkg/ovsdb/ovnnb"
)

// CreateLoadBalancer create loadbalancer
func (c *ovnClient) CreateLoadBalancer(lbName, protocol, selectFields string) error {
	exist, err := c.LoadBalancerExists(lbName)
	if err != nil {
		return err
	}

	// found, ignore
	if exist {
		return nil
	}

	lb := &ovnnb.LoadBalancer{
		UUID:     ovsclient.NamedUUID(),
		Name:     lbName,
		Protocol: &protocol,
	}

	if len(selectFields) != 0 {
		lb.SelectionFields = []string{selectFields}
	}

	op, err := c.ovnNbClient.Create(lb)
	if err != nil {
		return fmt.Errorf("generate operations for creating load balancer %s: %v", lbName, err)
	}

	if err := c.Transact("lb-add", op); err != nil {
		return fmt.Errorf("create load balancer %s: %v", lbName, err)
	}

	return nil
}

// UpdateLoadBalancer update load balancer
func (c *ovnClient) UpdateLoadBalancer(lb *ovnnb.LoadBalancer, fields ...interface{}) error {
	op, err := c.ovnNbClient.Where(lb).Update(lb, fields...)
	if err != nil {
		return fmt.Errorf("generate operations for updating load balancer %s: %v", lb.Name, err)
	}

	if err = c.Transact("lb-update", op); err != nil {
		return fmt.Errorf("update load balancer %s: %v", lb.Name, err)
	}

	return nil
}

// LoadBalancerAddVips add vips
func (c *ovnClient) LoadBalancerAddVips(lbName string, vips map[string]string) error {
	if len(vips) == 0 {
		return nil
	}

	lb, err := c.GetLoadBalancer(lbName, false)
	if err != nil {
		return err
	}

	updatedVips := make(map[string]string, len(lb.Vips)+1)
	for vip, backends := range lb.Vips {
		updatedVips[vip] = backends
	}
	for vip, backends := range vips {
		updatedVips[vip] = backends
	}
	lb.Vips = updatedVips

	if err := c.UpdateLoadBalancer(lb, &lb.Vips); err != nil {
		return fmt.Errorf("add vips %v to lb %s: %v", vips, lbName, err)
	}

	return nil
}

// LoadBalancerDeleteVips delete load balancer vips
func (c *ovnClient) LoadBalancerDeleteVips(lbName string, vips map[string]struct{}) error {
	if len(vips) == 0 {
		return nil
	}

	lb, err := c.GetLoadBalancer(lbName, false)
	if err != nil {
		return err
	}

	for vip := range vips {
		delete(lb.Vips, vip)
	}

	if err := c.UpdateLoadBalancer(lb, &lb.Vips); err != nil {
		return fmt.Errorf("delete vips %v from lb %s: %v", vips, lbName, err)
	}

	return nil
}

// SetLoadBalancerAffinityTimeout sets the LB's affinity timeout in seconds
func (c *ovnClient) SetLoadBalancerAffinityTimeout(lbName string, timeout int) error {
	lb, err := c.GetLoadBalancer(lbName, false)
	if err != nil {
		return err
	}

	if lb.Options == nil {
		lb.Options = make(map[string]string)
	}

	lb.Options["affinity_timeout"] = strconv.Itoa(timeout)

	if err := c.UpdateLoadBalancer(lb, &lb.Options); err != nil {
		return fmt.Errorf("set affinity timeout of lb %s to %d, err: %v", lbName, timeout, err)
	}

	return nil
}

// DeleteLoadBalancers delete several loadbalancer once
func (c *ovnClient) DeleteLoadBalancers(filter func(lb *ovnnb.LoadBalancer) bool) error {
	op, err := c.ovnNbClient.WhereCache(func(lb *ovnnb.LoadBalancer) bool {
		if filter != nil {
			return filter(lb)
		}

		return true
	}).Delete()

	if err != nil {
		return fmt.Errorf("generate operations for delete load balancers: %v", err)
	}

	if err := c.Transact("lb-del", op); err != nil {
		return fmt.Errorf("delete load balancers : %v", err)
	}

	return nil
}

// DeleteLoadBalancer delete loadbalancer
func (c *ovnClient) DeleteLoadBalancer(lbName string) error {
	op, err := c.DeleteLoadBalancerOp(lbName)
	if err != nil {
		return nil
	}

	if err := c.Transact("lb-del", op); err != nil {
		return fmt.Errorf("delete load balancer %s: %v", lbName, err)
	}

	return nil
}

// GetLoadBalancer get load balancer by name,
// it is because of lack name index that does't use ovnNbClient.Get
func (c *ovnClient) GetLoadBalancer(lbName string, ignoreNotFound bool) (*ovnnb.LoadBalancer, error) {
	lbList := make([]ovnnb.LoadBalancer, 0)
	if err := c.ovnNbClient.WhereCache(func(lb *ovnnb.LoadBalancer) bool {
		return lb.Name == lbName
	}).List(context.TODO(), &lbList); err != nil {
		return nil, fmt.Errorf("list load balancer %q: %v", lbName, err)
	}

	// not found
	if len(lbList) == 0 {
		if ignoreNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("not found load balancer %q", lbName)
	}

	if len(lbList) > 1 {
		return nil, fmt.Errorf("more than one load balancer with same name %q", lbName)
	}

	return &lbList[0], nil
}

func (c *ovnClient) LoadBalancerExists(lbName string) (bool, error) {
	lrp, err := c.GetLoadBalancer(lbName, true)
	return lrp != nil, err
}

// ListLoadBalancers list all load balancers
func (c *ovnClient) ListLoadBalancers(filter func(lb *ovnnb.LoadBalancer) bool) ([]ovnnb.LoadBalancer, error) {
	lbList := make([]ovnnb.LoadBalancer, 0)
	if err := c.ovnNbClient.WhereCache(func(lb *ovnnb.LoadBalancer) bool {
		if filter != nil {
			return filter(lb)
		}

		return true
	}).List(context.TODO(), &lbList); err != nil {
		return nil, fmt.Errorf("list load balancer: %v", err)
	}

	return lbList, nil
}

func (c *ovnClient) LoadBalancerOp(lbName string, mutationsFunc ...func(lb *ovnnb.LoadBalancer) *model.Mutation) ([]ovsdb.Operation, error) {
	lb, err := c.GetLoadBalancer(lbName, false)
	if err != nil {
		return nil, err
	}

	if len(mutationsFunc) == 0 {
		return nil, nil
	}

	mutations := make([]model.Mutation, 0, len(mutationsFunc))

	for _, f := range mutationsFunc {
		mutation := f(lb)

		if mutation != nil {
			mutations = append(mutations, *mutation)
		}
	}

	ops, err := c.ovnNbClient.Where(lb).Mutate(lb, mutations...)
	if err != nil {
		return nil, fmt.Errorf("generate operations for mutating load balancer %s: %v", lb.Name, err)
	}

	return ops, nil
}

// DeleteLoadBalancerOp create operation which delete load balancer
func (c *ovnClient) DeleteLoadBalancerOp(lbName string) ([]ovsdb.Operation, error) {
	lb, err := c.GetLoadBalancer(lbName, true)

	if err != nil {
		return nil, err
	}

	// not found, skip
	if lb == nil {
		return nil, nil
	}

	op, err := c.Where(lb).Delete()
	if err != nil {
		return nil, err
	}

	return op, nil
}

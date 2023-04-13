package ovs

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	kubeovnv1 "github.com/kubeovn/kube-ovn/pkg/apis/kubeovn/v1"
	ovsclient "github.com/kubeovn/kube-ovn/pkg/ovsdb/client"
	"github.com/kubeovn/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/kubeovn/kube-ovn/pkg/util"
)

func mockNetworkPolicyPort() []netv1.NetworkPolicyPort {
	protocolTcp := v1.ProtocolTCP
	var endPort int32 = 20000
	return []netv1.NetworkPolicyPort{
		{
			Port: &intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: 12345,
			},
			Protocol: &protocolTcp,
		},
		{
			Port: &intstr.IntOrString{
				Type:   intstr.Int,
				IntVal: 12346,
			},
			EndPort:  &endPort,
			Protocol: &protocolTcp,
		},
	}
}

func newAcl(parentName, direction, priority, match, action string, options ...func(acl *ovnnb.ACL)) *ovnnb.ACL {
	intPriority, _ := strconv.Atoi(priority)

	acl := &ovnnb.ACL{
		UUID:      ovsclient.NamedUUID(),
		Action:    action,
		Direction: direction,
		Match:     match,
		Priority:  intPriority,
		ExternalIDs: map[string]string{
			aclParentKey: parentName,
		},
	}

	for _, option := range options {
		option(acl)
	}

	return acl
}

func (suite *OvnClientTestSuite) testCreateIngressAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient

	t.Run("ipv4 acl", func(t *testing.T) {
		t.Parallel()

		pgName := "test_create_v4_ingress_acl_pg"
		asIngressName := "test.default.ingress.allow.ipv4"
		asExceptName := "test.default.ingress.except.ipv4"
		protocol := kubeovnv1.ProtocolIPv4

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		npp := mockNetworkPolicyPort()

		err = ovnClient.CreateIngressAcl(pgName, asIngressName, asExceptName, protocol, npp, true, nil)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 3)

		match := fmt.Sprintf("outport == @%s && ip", pgName)
		defaultDropAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, false)
		require.NoError(t, err)

		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Log = true
			acl.Severity = &ovnnb.ACLSeverityWarning
			acl.UUID = defaultDropAcl.UUID
		})

		require.Equal(t, expect, defaultDropAcl)
		require.Contains(t, pg.ACLs, defaultDropAcl.UUID)

		matches := newNetworkPolicyAclMatch(pgName, asIngressName, asExceptName, protocol, ovnnb.ACLDirectionToLport, npp, nil)
		for _, m := range matches {
			allowAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, false)
			require.NoError(t, err)

			expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, ovnnb.ACLActionAllowRelated)
			expect.UUID = allowAcl.UUID
			require.Equal(t, expect, allowAcl)

			require.Contains(t, pg.ACLs, allowAcl.UUID)
		}
	})

	t.Run("ipv6 acl", func(t *testing.T) {
		t.Parallel()

		pgName := "test_create_v6_ingress_acl_pg"
		asIngressName := "test.default.ingress.allow.ipv6"
		asExceptName := "test.default.ingress.except.ipv6"
		protocol := kubeovnv1.ProtocolIPv6

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		err = ovnClient.CreateIngressAcl(pgName, asIngressName, asExceptName, protocol, nil, true, nil)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 2)

		match := fmt.Sprintf("outport == @%s && ip", pgName)
		defaultDropAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, false)
		require.NoError(t, err)

		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Log = true
			acl.Severity = &ovnnb.ACLSeverityWarning
			acl.UUID = defaultDropAcl.UUID
		})

		require.Equal(t, expect, defaultDropAcl)
		require.Contains(t, pg.ACLs, defaultDropAcl.UUID)

		matches := newNetworkPolicyAclMatch(pgName, asIngressName, asExceptName, protocol, ovnnb.ACLDirectionToLport, nil, nil)
		for _, m := range matches {
			allowAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, false)
			require.NoError(t, err)

			expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, ovnnb.ACLActionAllowRelated)
			expect.UUID = allowAcl.UUID
			require.Equal(t, expect, allowAcl)

			require.Contains(t, pg.ACLs, allowAcl.UUID)
		}
	})

	t.Run("ipv4 acl with log is disabled", func(t *testing.T) {
		t.Parallel()

		pgName := "test_create_v4_ingress_acl_pg_with_log_disabled"
		asIngressName := "test.default.ingress.allow.ipv4.1"
		asExceptName := "test.default.ingress.except.ipv4.1"
		protocol := kubeovnv1.ProtocolIPv4

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		err = ovnClient.CreateIngressAcl(pgName, asIngressName, asExceptName, protocol, nil, false, nil)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 2)

		match := fmt.Sprintf("outport == @%s && ip", pgName)
		defaultDropAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, false)
		require.NoError(t, err)

		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.UUID = defaultDropAcl.UUID
		})

		require.Equal(t, expect, defaultDropAcl)
		require.Contains(t, pg.ACLs, defaultDropAcl.UUID)

		matches := newNetworkPolicyAclMatch(pgName, asIngressName, asExceptName, protocol, ovnnb.ACLDirectionToLport, nil, nil)
		for _, m := range matches {
			allowAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, false)
			require.NoError(t, err)

			expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, m, ovnnb.ACLActionAllowRelated)
			expect.UUID = allowAcl.UUID
			require.Equal(t, expect, allowAcl)

			require.Contains(t, pg.ACLs, allowAcl.UUID)
		}
	})
}

func (suite *OvnClientTestSuite) testCreateEgressAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient

	t.Run("ipv4 acl", func(t *testing.T) {
		t.Parallel()

		pgName := "test_create_v4_egress_acl_pg"
		asEgressName := "test.default.egress.allow.ipv4"
		asExceptName := "test.default.egress.except.ipv4"
		protocol := kubeovnv1.ProtocolIPv4

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		npp := mockNetworkPolicyPort()

		err = ovnClient.CreateEgressAcl(pgName, asEgressName, asExceptName, protocol, npp, true, nil)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 3)

		match := fmt.Sprintf("inport == @%s && ip", pgName)
		defaultDropAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressDefaultDrop, match, false)
		require.NoError(t, err)

		expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Log = true
			acl.Severity = &ovnnb.ACLSeverityWarning
			acl.UUID = defaultDropAcl.UUID
		})

		require.Equal(t, expect, defaultDropAcl)
		require.Contains(t, pg.ACLs, defaultDropAcl.UUID)

		matches := newNetworkPolicyAclMatch(pgName, asEgressName, asExceptName, protocol, ovnnb.ACLDirectionFromLport, npp, nil)
		for _, m := range matches {
			allowAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, m, false)
			require.NoError(t, err)

			expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, m, ovnnb.ACLActionAllowRelated)
			expect.UUID = allowAcl.UUID
			require.Equal(t, expect, allowAcl)

			require.Contains(t, pg.ACLs, allowAcl.UUID)
		}
	})

	t.Run("ipv6 acl", func(t *testing.T) {
		t.Parallel()

		pgName := "test_create_v6_egress_acl_pg"
		asEgressName := "test.default.egress.allow.ipv6"
		asExceptName := "test.default.egress.except.ipv6"
		protocol := kubeovnv1.ProtocolIPv6

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		err = ovnClient.CreateEgressAcl(pgName, asEgressName, asExceptName, protocol, nil, true, nil)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 2)

		match := fmt.Sprintf("inport == @%s && ip", pgName)
		defaultDropAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressDefaultDrop, match, false)
		require.NoError(t, err)

		expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Log = true
			acl.Severity = &ovnnb.ACLSeverityWarning
			acl.UUID = defaultDropAcl.UUID
		})

		require.Equal(t, expect, defaultDropAcl)
		require.Contains(t, pg.ACLs, defaultDropAcl.UUID)

		matches := newNetworkPolicyAclMatch(pgName, asEgressName, asExceptName, protocol, ovnnb.ACLDirectionFromLport, nil, nil)
		for _, m := range matches {
			allowAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, m, false)
			require.NoError(t, err)

			expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, m, ovnnb.ACLActionAllowRelated)
			expect.UUID = allowAcl.UUID
			require.Equal(t, expect, allowAcl)

			require.Contains(t, pg.ACLs, allowAcl.UUID)
		}
	})
}

func (suite *OvnClientTestSuite) testCreateGatewayAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient

	checkAcl := func(parent interface{}, direction, priority, match string, options map[string]string) {
		pg, isPg := parent.(*ovnnb.PortGroup)
		var name string
		var acls []string

		if isPg {
			name = pg.Name
			acls = pg.ACLs
		} else {
			ls := parent.(*ovnnb.LogicalSwitch)
			name = ls.Name
			acls = ls.ACLs
		}

		acl, err := ovnClient.GetAcl(name, direction, priority, match, false)
		require.NoError(t, err)
		expect := newAcl(name, direction, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		if len(options) != 0 {
			expect.Options = options
		}
		require.Equal(t, expect, acl)
		require.Contains(t, acls, acl.UUID)

	}

	expect := func(parent interface{}, gateway string) {
		for _, gw := range strings.Split(gateway, ",") {
			protocol := util.CheckProtocol(gw)
			ipSuffix := "ip4"
			if protocol == kubeovnv1.ProtocolIPv6 {
				ipSuffix = "ip6"
			}

			match := fmt.Sprintf("%s.src == %s", ipSuffix, gw)
			checkAcl(parent, ovnnb.ACLDirectionToLport, util.IngressAllowPriority, match, nil)

			match = fmt.Sprintf("%s.dst == %s", ipSuffix, gw)
			checkAcl(parent, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, match, nil)

			if ipSuffix == "ip6" {
				match = "nd || nd_ra || nd_rs"
				checkAcl(parent, ovnnb.ACLDirectionFromLport, util.EgressAllowPriority, match, map[string]string{
					"apply-after-lb": "true",
				})
			}
		}
	}

	t.Run("add acl to pg", func(t *testing.T) {
		t.Parallel()

		t.Run("gateway's protocol is dual", func(t *testing.T) {
			t.Parallel()

			pgName := "test_create_gw_acl_pg_dual"
			gateway := "10.244.0.1,fc00::0af4:01"

			err := ovnClient.CreatePortGroup(pgName, nil)
			require.NoError(t, err)

			err = ovnClient.CreateGatewayAcl("", pgName, gateway)
			require.NoError(t, err)

			pg, err := ovnClient.GetPortGroup(pgName, false)
			require.NoError(t, err)
			require.Len(t, pg.ACLs, 5)

			expect(pg, gateway)
		})

		t.Run("gateway's protocol is ipv4", func(t *testing.T) {
			t.Parallel()

			pgName := "test_create_gw_acl_pg_v4"
			gateway := "10.244.0.1"

			err := ovnClient.CreatePortGroup(pgName, nil)
			require.NoError(t, err)

			err = ovnClient.CreateGatewayAcl("", pgName, gateway)
			require.NoError(t, err)

			pg, err := ovnClient.GetPortGroup(pgName, false)
			require.NoError(t, err)
			require.Len(t, pg.ACLs, 2)

			expect(pg, gateway)
		})

		t.Run("gateway's protocol is ipv6", func(t *testing.T) {
			t.Parallel()

			pgName := "test_create_gw_acl_pg_v6"
			gateway := "fc00::0af4:01"

			err := ovnClient.CreatePortGroup(pgName, nil)
			require.NoError(t, err)

			err = ovnClient.CreateGatewayAcl("", pgName, gateway)
			require.NoError(t, err)

			pg, err := ovnClient.GetPortGroup(pgName, false)
			require.NoError(t, err)
			require.Len(t, pg.ACLs, 3)

			expect(pg, gateway)
		})
	})

	t.Run("add acl to ls", func(t *testing.T) {
		t.Parallel()

		t.Run("gateway's protocol is dual", func(t *testing.T) {
			t.Parallel()

			lsName := "test_create_gw_acl_ls_dual"
			gateway := "10.244.0.1,fc00::0af4:01"

			err := ovnClient.CreateBareLogicalSwitch(lsName)
			require.NoError(t, err)

			err = ovnClient.CreateGatewayAcl(lsName, "", gateway)
			require.NoError(t, err)

			ls, err := ovnClient.GetLogicalSwitch(lsName, false)
			require.NoError(t, err)
			require.Len(t, ls.ACLs, 5)

			expect(ls, gateway)
		})
	})

	t.Run("has no pg name and ls name", func(t *testing.T) {
		t.Parallel()
		err := ovnClient.CreateGatewayAcl("", "", "")
		require.EqualError(t, err, "one of port group name and logical switch name must be specified")
	})
}

func (suite *OvnClientTestSuite) testCreateNodeAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test_create_node_acl_pg"
	nodeIp := "192.168.20.3"
	joinIp := "100.64.0.2,fd00:100:64::2"

	checkAcl := func(pg *ovnnb.PortGroup, direction, priority, match string, options map[string]string) {
		acl, err := ovnClient.GetAcl(pg.Name, direction, priority, match, false)
		require.NoError(t, err)
		expect := newAcl(pg.Name, direction, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		if len(options) != 0 {
			expect.Options = options
		}
		require.Equal(t, expect, acl)
		require.Contains(t, pg.ACLs, acl.UUID)
	}

	expect := func(pg *ovnnb.PortGroup, gateway string) {
		for _, ip := range strings.Split(nodeIp, ",") {
			protocol := util.CheckProtocol(ip)
			ipSuffix := "ip4"
			if protocol == kubeovnv1.ProtocolIPv6 {
				ipSuffix = "ip6"
			}

			pgAs := fmt.Sprintf("%s_%s", pgName, ipSuffix)

			match := fmt.Sprintf("%s.src == %s && %s.dst == $%s", ipSuffix, ip, ipSuffix, pgAs)
			checkAcl(pg, ovnnb.ACLDirectionToLport, util.NodeAllowPriority, match, nil)

			match = fmt.Sprintf("%s.dst == %s && %s.src == $%s", ipSuffix, ip, ipSuffix, pgAs)
			checkAcl(pg, ovnnb.ACLDirectionFromLport, util.NodeAllowPriority, match, map[string]string{
				"apply-after-lb": "true",
			})
		}
	}

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	err = ovnClient.CreateNodeAcl(pgName, nodeIp, joinIp)
	require.NoError(t, err)

	pg, err := ovnClient.GetPortGroup(pgName, false)
	require.NoError(t, err)
	require.Len(t, pg.ACLs, 2)

	expect(pg, nodeIp)
}

func (suite *OvnClientTestSuite) testCreateSgDenyAllAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	sgName := "test_create_deny_all_acl_pg"
	pgName := GetSgPortGroupName(sgName)

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	err = ovnClient.CreateSgDenyAllAcl(sgName)
	require.NoError(t, err)

	pg, err := ovnClient.GetPortGroup(pgName, false)
	require.NoError(t, err)

	// ingress acl
	match := fmt.Sprintf("outport == @%s && ip", pgName)
	ingressAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupDropPriority, match, false)
	require.NoError(t, err)
	expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupDropPriority, match, ovnnb.ACLActionDrop)
	expect.UUID = ingressAcl.UUID
	require.Equal(t, expect, ingressAcl)
	require.Contains(t, pg.ACLs, ingressAcl.UUID)

	// egress acl
	match = fmt.Sprintf("inport == @%s && ip", pgName)
	egressAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupDropPriority, match, false)
	require.NoError(t, err)
	expect = newAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupDropPriority, match, ovnnb.ACLActionDrop)
	expect.UUID = egressAcl.UUID
	require.Equal(t, expect, egressAcl)
	require.Contains(t, pg.ACLs, egressAcl.UUID)
}

func (suite *OvnClientTestSuite) testCreateSgBaseACL() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient

	expect := func(pg *ovnnb.PortGroup, match string) {
		arpAcl, err := ovnClient.GetAcl(pg.Name, ovnnb.ACLDirectionToLport, util.SecurityGroupBasePriority, match, false)
		require.NoError(t, err)

		expect := newAcl(pg.Name, ovnnb.ACLDirectionToLport, util.SecurityGroupBasePriority, match, ovnnb.ACLActionAllowRelated, func(acl *ovnnb.ACL) {
			acl.UUID = arpAcl.UUID
		})

		require.Equal(t, expect, arpAcl)
		require.Contains(t, pg.ACLs, arpAcl.UUID)
	}

	t.Run("create sg base ingress acl", func(t *testing.T) {
		t.Parallel()

		sgName := "test_create_sg_base_ingress_acl"
		pgName := GetSgPortGroupName(sgName)
		portDirection := "outport"

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		// ingress
		err = ovnClient.CreateSgBaseACL(sgName, ovnnb.ACLDirectionToLport)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 4)

		// arp
		match := fmt.Sprintf("%s == @%s && arp", portDirection, pgName)
		expect(pg, match)

		// icmpv6
		match = fmt.Sprintf("%s == @%s && icmp6.type == {130, 134, 135, 136} && icmp6.code == 0 && ip.ttl == 255", portDirection, pgName)
		expect(pg, match)

		// dhcpv4
		match = fmt.Sprintf("%s == @%s && udp.src == 67 && udp.dst == 68 && ip4", portDirection, pgName)
		expect(pg, match)

		// dhcpv6
		match = fmt.Sprintf("%s == @%s && udp.src == 547 && udp.dst == 546 && ip6", portDirection, pgName)
		expect(pg, match)
	})

	t.Run("create sg base egress acl", func(t *testing.T) {
		t.Parallel()

		sgName := "test_create_sg_base_egress_acl"
		pgName := GetSgPortGroupName(sgName)
		portDirection := "inport"

		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		// egress
		err = ovnClient.CreateSgBaseACL(sgName, ovnnb.ACLDirectionFromLport)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 4)

		// arp
		match := fmt.Sprintf("%s == @%s && arp", portDirection, pgName)
		expect(pg, match)

		// icmpv6
		match = fmt.Sprintf("%s == @%s && icmp6.type == {130, 134, 135, 136} && icmp6.code == 0 && ip.ttl == 255", portDirection, pgName)
		expect(pg, match)

		// dhcpv4
		match = fmt.Sprintf("%s == @%s && udp.src == 68 && udp.dst == 67 && ip4", portDirection, pgName)
		expect(pg, match)

		// dhcpv6
		match = fmt.Sprintf("%s == @%s && udp.src == 546 && udp.dst == 547 && ip6", portDirection, pgName)
		expect(pg, match)
	})

}

func (suite *OvnClientTestSuite) testUpdateSgAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	sgName := "test_update_sg_acl_pg"
	v4AsName := GetSgV4AssociatedName(sgName)
	v6AsName := GetSgV6AssociatedName(sgName)
	pgName := GetSgPortGroupName(sgName)

	sg := &kubeovnv1.SecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: sgName,
		},
		Spec: kubeovnv1.SecurityGroupSpec{
			AllowSameGroupTraffic: true,
			IngressRules: []*kubeovnv1.SgRule{
				{
					IPVersion:     "ipv4",
					RemoteType:    kubeovnv1.SgRemoteTypeAddress,
					RemoteAddress: "0.0.0.0/0",
					Protocol:      "icmp",
					Priority:      12,
					Policy:        "allow",
				},
			},
			EgressRules: []*kubeovnv1.SgRule{
				{
					IPVersion:     "ipv4",
					RemoteType:    kubeovnv1.SgRemoteTypeAddress,
					RemoteAddress: "0.0.0.0/0",
					Protocol:      "all",
					Priority:      10,
					Policy:        "allow",
				},
			},
		},
	}

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	t.Run("update securityGroup ingress acl", func(t *testing.T) {
		err = ovnClient.UpdateSgAcl(sg, ovnnb.ACLDirectionToLport)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)

		// ipv4 acl
		match := fmt.Sprintf("outport == @%s && ip4 && ip4.src == $%s", pgName, v4AsName)
		v4Acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupAllowPriority, match, false)
		require.NoError(t, err)
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupAllowPriority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = v4Acl.UUID
		require.Equal(t, expect, v4Acl)
		require.Contains(t, pg.ACLs, v4Acl.UUID)

		// ipv6 acl
		match = fmt.Sprintf("outport == @%s && ip6 && ip6.src == $%s", pgName, v6AsName)
		v6Acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupAllowPriority, match, false)
		require.NoError(t, err)
		expect = newAcl(pgName, ovnnb.ACLDirectionToLport, util.SecurityGroupAllowPriority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = v6Acl.UUID
		require.Equal(t, expect, v6Acl)
		require.Contains(t, pg.ACLs, v6Acl.UUID)

		// rule acl
		match = fmt.Sprintf("outport == @%s && ip4 && ip4.src == 0.0.0.0/0 && icmp4", pgName)
		rulAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, "2288", match, false)
		require.NoError(t, err)
		expect = newAcl(pgName, ovnnb.ACLDirectionToLport, "2288", match, ovnnb.ACLActionAllowRelated)
		expect.UUID = rulAcl.UUID
		require.Equal(t, expect, rulAcl)
		require.Contains(t, pg.ACLs, rulAcl.UUID)
	})

	t.Run("update securityGroup egress acl", func(t *testing.T) {
		err = ovnClient.UpdateSgAcl(sg, ovnnb.ACLDirectionFromLport)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)

		// ipv4 acl
		match := fmt.Sprintf("inport == @%s && ip4 && ip4.dst == $%s", pgName, v4AsName)
		v4Acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupAllowPriority, match, false)
		require.NoError(t, err)
		expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupAllowPriority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = v4Acl.UUID
		require.Equal(t, expect, v4Acl)
		require.Contains(t, pg.ACLs, v4Acl.UUID)

		// ipv6 acl
		match = fmt.Sprintf("inport == @%s && ip6 && ip6.dst == $%s", pgName, v6AsName)
		v6Acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupAllowPriority, match, false)
		require.NoError(t, err)
		expect = newAcl(pgName, ovnnb.ACLDirectionFromLport, util.SecurityGroupAllowPriority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = v6Acl.UUID
		require.Equal(t, expect, v6Acl)
		require.Contains(t, pg.ACLs, v6Acl.UUID)

		// rule acl
		match = fmt.Sprintf("inport == @%s && ip4 && ip4.dst == 0.0.0.0/0", pgName)
		rulAcl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, "2290", match, false)
		require.NoError(t, err)
		expect = newAcl(pgName, ovnnb.ACLDirectionFromLport, "2290", match, ovnnb.ACLActionAllowRelated)
		expect.UUID = rulAcl.UUID
		require.Equal(t, expect, rulAcl)
		require.Contains(t, pg.ACLs, rulAcl.UUID)
	})
}

func (suite *OvnClientTestSuite) testUpdateLogicalSwitchAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	lsName := "test_update_acl_ls"

	subnetAcls := []kubeovnv1.Acl{
		{
			Direction: ovnnb.ACLDirectionToLport,
			Priority:  1111,
			Match:     "ip4.src == 192.168.111.5",
			Action:    ovnnb.ACLActionAllowRelated,
		},
		{
			Direction: ovnnb.ACLDirectionFromLport,
			Priority:  1111,
			Match:     "ip4.dst == 192.168.111.50",
			Action:    ovnnb.ACLActionDrop,
		},
	}

	err := ovnClient.CreateBareLogicalSwitch(lsName)
	require.NoError(t, err)

	err = ovnClient.UpdateLogicalSwitchAcl(lsName, subnetAcls)
	require.NoError(t, err)

	ls, err := ovnClient.GetLogicalSwitch(lsName, false)
	require.NoError(t, err)

	for _, subnetAcl := range subnetAcls {
		acl, err := ovnClient.GetAcl(lsName, subnetAcl.Direction, strconv.Itoa(subnetAcl.Priority), subnetAcl.Match, false)
		require.NoError(t, err)
		expect := newAcl(lsName, subnetAcl.Direction, strconv.Itoa(subnetAcl.Priority), subnetAcl.Match, subnetAcl.Action)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
		require.Contains(t, ls.ACLs, acl.UUID)
	}
}

func (suite *OvnClientTestSuite) testSetAclLog() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := GetSgPortGroupName("test_set_acl_log")

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	t.Run("set ingress acl log to false", func(t *testing.T) {
		match := fmt.Sprintf("outport == @%s && ip", pgName)
		acl := newAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Name = &pgName
			acl.Log = true
			acl.Severity = &ovnnb.ACLSeverityWarning
		})

		err = ovnClient.CreateAcls(pgName, portGroupKey, acl)
		require.NoError(t, err)

		err = ovnClient.SetAclLog(pgName, false, true)
		require.NoError(t, err)

		acl, err = ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, util.IngressDefaultDrop, match, false)
		require.NoError(t, err)
		require.False(t, acl.Log)
	})

	t.Run("set egress acl log to false", func(t *testing.T) {
		match := fmt.Sprintf("inport == @%s && ip", pgName)
		acl := newAcl(pgName, ovnnb.ACLDirectionFromLport, util.IngressDefaultDrop, match, ovnnb.ACLActionDrop, func(acl *ovnnb.ACL) {
			acl.Name = &pgName
			acl.Log = false
			acl.Severity = &ovnnb.ACLSeverityWarning
		})

		err = ovnClient.CreateAcls(pgName, portGroupKey, acl)
		require.NoError(t, err)

		err = ovnClient.SetAclLog(pgName, true, false)
		require.NoError(t, err)

		acl, err = ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, util.IngressDefaultDrop, match, false)
		require.NoError(t, err)
		require.True(t, acl.Log)
	})

}

func (suite *OvnClientTestSuite) testSetLogicalSwitchPrivate() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	lsName := "test_set_private_ls"
	cidrBlock := "10.244.0.0/16,fc00::af4:0/112"
	allowSubnets := []string{
		"10.230.0.0/16",
		"10.240.0.0/16",
		"fc00::af9:0/112",
		"fc00::afa:0/112",
	}
	direction := ovnnb.ACLDirectionToLport

	err := ovnClient.CreateBareLogicalSwitch(lsName)
	require.NoError(t, err)

	t.Run("subnet protocol is dual", func(t *testing.T) {
		err = ovnClient.SetLogicalSwitchPrivate(lsName, cidrBlock, allowSubnets)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 9)

		// default drop acl
		match := "ip"
		acl, err := ovnClient.GetAcl(lsName, direction, util.DefaultDropPriority, match, false)
		require.NoError(t, err)
		require.Contains(t, ls.ACLs, acl.UUID)

		// same subnet acl
		for _, cidr := range strings.Split(cidrBlock, ",") {
			protocol := util.CheckProtocol(cidr)

			match := fmt.Sprintf(`ip4.src == %s && ip4.dst == %s`, cidr, cidr)
			if protocol == kubeovnv1.ProtocolIPv6 {
				match = fmt.Sprintf(`ip6.src == %s && ip6.dst == %s`, cidr, cidr)
			}

			acl, err = ovnClient.GetAcl(lsName, direction, util.SubnetAllowPriority, match, false)
			require.NoError(t, err)
			require.Contains(t, ls.ACLs, acl.UUID)

			// allow subnet acl
			for _, subnet := range allowSubnets {
				protocol := util.CheckProtocol(cidr)

				allowProtocol := util.CheckProtocol(subnet)
				if allowProtocol != protocol {
					continue
				}

				match = fmt.Sprintf("(ip4.src == %s && ip4.dst == %s) || (ip4.src == %s && ip4.dst == %s)", cidr, subnet, subnet, cidr)
				if protocol == kubeovnv1.ProtocolIPv6 {
					match = fmt.Sprintf("(ip6.src == %s && ip6.dst == %s) || (ip6.src == %s && ip6.dst == %s)", cidr, subnet, subnet, cidr)
				}

				acl, err = ovnClient.GetAcl(lsName, direction, util.SubnetAllowPriority, match, false)
				require.NoError(t, err)
				require.Contains(t, ls.ACLs, acl.UUID)
			}
		}

		// node subnet acl
		for _, cidr := range strings.Split(ovnClient.NodeSwitchCIDR, ",") {
			protocol := util.CheckProtocol(cidr)

			match := fmt.Sprintf(`ip4.src == %s`, cidr)
			if protocol == kubeovnv1.ProtocolIPv6 {
				match = fmt.Sprintf(`ip6.src == %s`, cidr)
			}

			acl, err = ovnClient.GetAcl(lsName, direction, util.NodeAllowPriority, match, false)
			require.NoError(t, err)
			require.Contains(t, ls.ACLs, acl.UUID)
		}
	})

	t.Run("subnet protocol is ipv4", func(t *testing.T) {
		cidrBlock := "10.244.0.0/16"
		err = ovnClient.SetLogicalSwitchPrivate(lsName, cidrBlock, allowSubnets)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 5)

		// default drop acl
		match := "ip"
		acl, err := ovnClient.GetAcl(lsName, direction, util.DefaultDropPriority, match, false)
		require.NoError(t, err)
		require.Contains(t, ls.ACLs, acl.UUID)

		// same subnet acl
		for _, cidr := range strings.Split(cidrBlock, ",") {
			protocol := util.CheckProtocol(cidr)

			match := fmt.Sprintf(`ip4.src == %s && ip4.dst == %s`, cidr, cidr)
			if protocol == kubeovnv1.ProtocolIPv6 {
				match = fmt.Sprintf(`ip6.src == %s && ip6.dst == %s`, cidr, cidr)
			}

			acl, err = ovnClient.GetAcl(lsName, direction, util.SubnetAllowPriority, match, false)
			require.NoError(t, err)
			require.Contains(t, ls.ACLs, acl.UUID)

			// allow subnet acl
			for _, subnet := range allowSubnets {
				protocol := util.CheckProtocol(cidr)

				allowProtocol := util.CheckProtocol(subnet)
				if allowProtocol != protocol {
					continue
				}

				match = fmt.Sprintf("(ip4.src == %s && ip4.dst == %s) || (ip4.src == %s && ip4.dst == %s)", cidr, subnet, subnet, cidr)
				if protocol == kubeovnv1.ProtocolIPv6 {
					match = fmt.Sprintf("(ip6.src == %s && ip6.dst == %s) || (ip6.src == %s && ip6.dst == %s)", cidr, subnet, subnet, cidr)
				}

				acl, err = ovnClient.GetAcl(lsName, direction, util.SubnetAllowPriority, match, false)
				require.NoError(t, err)
				require.Contains(t, ls.ACLs, acl.UUID)
			}
		}

		// node subnet acl
		for _, cidr := range strings.Split(ovnClient.NodeSwitchCIDR, ",") {
			protocol := util.CheckProtocol(cidr)

			match := fmt.Sprintf(`ip4.src == %s`, cidr)
			if protocol == kubeovnv1.ProtocolIPv6 {
				match = fmt.Sprintf(`ip6.src == %s`, cidr)
			}

			acl, err = ovnClient.GetAcl(lsName, direction, util.NodeAllowPriority, match, false)
			if protocol == kubeovnv1.ProtocolIPv4 {
				require.NoError(t, err)
				require.Contains(t, ls.ACLs, acl.UUID)
			} else {
				require.ErrorContains(t, err, "not found acl")
			}
		}
	})
}

func (suite *OvnClientTestSuite) test_newSgRuleACL() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	sgName := "test_create_sg_acl_pg"
	pgName := GetSgPortGroupName(sgName)
	highestPriority, _ := strconv.Atoi(util.SecurityGroupHighestPriority)

	t.Run("create securityGroup type sg acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:           "ipv4",
			RemoteType:          kubeovnv1.SgRemoteTypeSg,
			RemoteSecurityGroup: "ovn.sg.test_sg",
			Protocol:            "icmp",
			Priority:            12,
			Policy:              "allow",
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionToLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("outport == @%s && ip4 && ip4.src == $%s && icmp4", pgName, GetSgV4AssociatedName(sgRule.RemoteSecurityGroup))
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})

	t.Run("create address type sg acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:     "ipv4",
			RemoteType:    kubeovnv1.SgRemoteTypeAddress,
			RemoteAddress: "10.10.10.12/24",
			Protocol:      "icmp",
			Priority:      12,
			Policy:        "allow",
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionToLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("outport == @%s && ip4 && ip4.src == %s && icmp4", pgName, sgRule.RemoteAddress)
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})

	t.Run("create ipv6 acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:     "ipv6",
			RemoteType:    kubeovnv1.SgRemoteTypeAddress,
			RemoteAddress: "fe80::200:ff:fe04:2611/64",
			Protocol:      "icmp",
			Priority:      12,
			Policy:        "allow",
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionToLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("outport == @%s && ip6 && ip6.src == %s && icmp6", pgName, sgRule.RemoteAddress)
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})

	t.Run("create egress sg acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:     "ipv4",
			RemoteType:    kubeovnv1.SgRemoteTypeAddress,
			RemoteAddress: "10.10.10.12/24",
			Protocol:      "icmp",
			Priority:      12,
			Policy:        "allow",
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionFromLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("inport == @%s && ip4 && ip4.dst == %s && icmp4", pgName, sgRule.RemoteAddress)
		expect := newAcl(pgName, ovnnb.ACLDirectionFromLport, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})

	t.Run("create drop sg acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:     "ipv4",
			RemoteType:    kubeovnv1.SgRemoteTypeAddress,
			RemoteAddress: "10.10.10.12/24",
			Protocol:      "icmp",
			Priority:      21,
			Policy:        "drop",
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionToLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("outport == @%s && ip4 && ip4.src == %s && icmp4", pgName, sgRule.RemoteAddress)
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionDrop)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})

	t.Run("create tcp sg acl", func(t *testing.T) {
		t.Parallel()

		sgRule := &kubeovnv1.SgRule{
			IPVersion:     "ipv4",
			RemoteType:    kubeovnv1.SgRemoteTypeAddress,
			RemoteAddress: "10.10.10.12/24",
			Protocol:      "tcp",
			Priority:      12,
			Policy:        "allow",
			PortRangeMin:  12345,
			PortRangeMax:  12360,
		}
		priority := strconv.Itoa(highestPriority - sgRule.Priority)

		acl, err := ovnClient.newSgRuleACL(sgName, ovnnb.ACLDirectionToLport, sgRule)
		require.NoError(t, err)

		match := fmt.Sprintf("outport == @%s && ip4 && ip4.src == %s && %d <= tcp.dst <= %d", pgName, sgRule.RemoteAddress, sgRule.PortRangeMin, sgRule.PortRangeMax)
		expect := newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		expect.UUID = acl.UUID
		require.Equal(t, expect, acl)
	})
}

func (suite *OvnClientTestSuite) testCreateAcls() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test-create-acls-pg"
	priority := "5000"
	basePort := 12300
	matchPrefix := "outport == @ovn.sg.test_create_acl_pg && ip"
	acls := make([]*ovnnb.ACL, 0, 3)

	t.Run("add acl to port group", func(t *testing.T) {
		err := ovnClient.CreatePortGroup(pgName, nil)
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(pgName, portGroupKey, append(acls, nil)...)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, false)
			require.NoError(t, err)
			require.Equal(t, match, acl.Match)

			require.Contains(t, pg.ACLs, acl.UUID)
		}
	})

	t.Run("add acl to logical switch", func(t *testing.T) {
		lsName := "test-create-acls-ls"
		err := ovnClient.CreateBareLogicalSwitch(lsName)
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(lsName, logicalSwitchKey, append(acls, nil)...)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.GetAcl(lsName, ovnnb.ACLDirectionToLport, priority, match, false)
			require.NoError(t, err)
			require.Equal(t, match, acl.Match)

			require.Contains(t, ls.ACLs, acl.UUID)
		}
	})

	t.Run("acl parent type is wrong", func(t *testing.T) {
		err := ovnClient.CreateAcls(pgName, "", nil)
		require.ErrorContains(t, err, "acl parent type must be 'pg' or 'ls'")

		err = ovnClient.CreateAcls(pgName, "wrong_key", nil)
		require.ErrorContains(t, err, "acl parent type must be 'pg' or 'ls'")
	})
}

func (suite *OvnClientTestSuite) testDeleteAcls() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test-del-acls-pg"
	lsName := "test-del-acls-ls"
	matchPrefix := "outport == @ovn.sg.test_del_acl_pg && ip"

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	err = ovnClient.CreateBareLogicalSwitch(lsName)
	require.NoError(t, err)

	t.Run("delete all direction acls from port group", func(t *testing.T) {
		priority := "5601"
		basePort := 5601
		acls := make([]*ovnnb.ACL, 0, 5)

		// to-lport
		for i := 0; i < 2; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		// from-lport
		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionFromLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(pgName, portGroupKey, acls...)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 5)

		err = ovnClient.DeleteAcls(pgName, portGroupKey, "")
		require.NoError(t, err)

		pg, err = ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Empty(t, pg.ACLs)
	})

	t.Run("delete one-way acls from port group", func(t *testing.T) {
		priority := "5701"
		basePort := 5701
		acls := make([]*ovnnb.ACL, 0, 5)

		// to-lport
		for i := 0; i < 2; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		// from-lport
		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionFromLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(pgName, portGroupKey, acls...)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 5)

		/* delete to-lport direction acl */
		err = ovnClient.DeleteAcls(pgName, portGroupKey, ovnnb.ACLDirectionToLport)
		require.NoError(t, err)

		pg, err = ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 3)

		/* delete from-lport direction acl */
		err = ovnClient.DeleteAcls(pgName, portGroupKey, ovnnb.ACLDirectionFromLport)
		require.NoError(t, err)

		pg, err = ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Empty(t, pg.ACLs)
	})

	t.Run("delete all direction acls from logical switch", func(t *testing.T) {
		priority := "5601"
		basePort := 5601
		acls := make([]*ovnnb.ACL, 0, 5)

		// to-lport
		for i := 0; i < 2; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		// from-lport
		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionFromLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(lsName, logicalSwitchKey, acls...)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 5)

		err = ovnClient.DeleteAcls(lsName, logicalSwitchKey, "")
		require.NoError(t, err)

		ls, err = ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Empty(t, ls.ACLs)
	})

	t.Run("delete one-way acls from logical switch", func(t *testing.T) {
		priority := "5701"
		basePort := 5701
		acls := make([]*ovnnb.ACL, 0, 5)

		// to-lport
		for i := 0; i < 2; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		// from-lport
		for i := 0; i < 3; i++ {
			match := fmt.Sprintf("%s && udp.dst == %d", matchPrefix, basePort+i)
			acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionFromLport, priority, match, ovnnb.ACLActionAllowRelated)
			require.NoError(t, err)
			acls = append(acls, acl)
		}

		err = ovnClient.CreateAcls(lsName, logicalSwitchKey, acls...)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 5)

		/* delete to-lport direction acl */
		err = ovnClient.DeleteAcls(lsName, logicalSwitchKey, ovnnb.ACLDirectionToLport)
		require.NoError(t, err)

		ls, err = ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 3)

		/* delete from-lport direction acl */
		err = ovnClient.DeleteAcls(lsName, logicalSwitchKey, ovnnb.ACLDirectionFromLport)
		require.NoError(t, err)

		ls, err = ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Empty(t, ls.ACLs)
	})
}

func (suite *OvnClientTestSuite) testDeleteAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test-del-acl-pg"
	lsName := "test-del-acl-ls"
	matchPrefix := "outport == @ovn.sg.test_del_acl_pg && ip"

	err := ovnClient.CreatePortGroup(pgName, nil)
	require.NoError(t, err)

	err = ovnClient.CreateBareLogicalSwitch(lsName)
	require.NoError(t, err)

	t.Run("delete acl from port group", func(t *testing.T) {
		priority := "5601"
		basePort := 5601

		match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort)
		acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		require.NoError(t, err)

		err = ovnClient.CreateAcls(pgName, portGroupKey, acl)
		require.NoError(t, err)

		pg, err := ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Len(t, pg.ACLs, 1)

		err = ovnClient.DeleteAcl(pgName, portGroupKey, ovnnb.ACLDirectionToLport, priority, match)
		require.NoError(t, err)

		pg, err = ovnClient.GetPortGroup(pgName, false)
		require.NoError(t, err)
		require.Empty(t, pg.ACLs)
	})

	t.Run("delete all direction acls from logical switch", func(t *testing.T) {
		priority := "5601"
		basePort := 5601

		match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort)
		acl, err := ovnClient.newAcl(lsName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
		require.NoError(t, err)

		err = ovnClient.CreateAcls(lsName, logicalSwitchKey, acl)
		require.NoError(t, err)

		ls, err := ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Len(t, ls.ACLs, 1)

		err = ovnClient.DeleteAcl(lsName, logicalSwitchKey, ovnnb.ACLDirectionToLport, priority, match)
		require.NoError(t, err)

		ls, err = ovnClient.GetLogicalSwitch(lsName, false)
		require.NoError(t, err)
		require.Empty(t, ls.ACLs)
	})
}

func (suite *OvnClientTestSuite) testGetAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test_get_acl_pg"
	priority := "2000"
	match := "ip4.dst == 100.64.0.0/16"

	err := ovnClient.CreateBareAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated)
	require.NoError(t, err)

	t.Run("direction, priority and match are same", func(t *testing.T) {
		t.Parallel()
		acl, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, false)
		require.NoError(t, err)
		require.Equal(t, ovnnb.ACLDirectionToLport, acl.Direction)
		require.Equal(t, 2000, acl.Priority)
		require.Equal(t, match, acl.Match)
		require.Equal(t, ovnnb.ACLActionAllowRelated, acl.Action)
	})

	t.Run("direction, priority and match are not all same", func(t *testing.T) {
		t.Parallel()

		_, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, priority, match, false)
		require.ErrorContains(t, err, "not found acl")

		_, err = ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, "1010", match, false)
		require.ErrorContains(t, err, "not found acl")

		_, err = ovnClient.GetAcl(pgName, ovnnb.ACLDirectionToLport, priority, match+" && tcp", false)
		require.ErrorContains(t, err, "not found acl")
	})

	t.Run("should no err when direction, priority and match are not all same but ignoreNotFound=true", func(t *testing.T) {
		t.Parallel()

		_, err := ovnClient.GetAcl(pgName, ovnnb.ACLDirectionFromLport, priority, match, true)
		require.NoError(t, err)
	})

	t.Run("no acl belongs to parent exist", func(t *testing.T) {
		t.Parallel()

		_, err := ovnClient.GetAcl(pgName+"_1", ovnnb.ACLDirectionFromLport, priority, match, false)
		require.ErrorContains(t, err, "not found acl")
	})
}

func (suite *OvnClientTestSuite) testListAcls() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test-list-acl-pg"
	basePort := 50000

	matchPrefix := "outport == @ovn.sg.test_list_acl_pg && ip"
	// create two to-lport acl
	for i := 0; i < 2; i++ {
		match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
		err := ovnClient.CreateBareAcl(pgName, ovnnb.ACLDirectionToLport, "9999", match, ovnnb.ACLActionAllowRelated)
		require.NoError(t, err)
	}

	// create two from-lport acl
	for i := 0; i < 3; i++ {
		match := fmt.Sprintf("%s && tcp.dst == %d", matchPrefix, basePort+i)
		err := ovnClient.CreateBareAcl(pgName, ovnnb.ACLDirectionFromLport, "9999", match, ovnnb.ACLActionAllowRelated)
		require.NoError(t, err)
	}

	/* list all direction acl */
	out, err := ovnClient.ListAcls("", nil)
	require.NoError(t, err)
	count := 0
	for _, v := range out {
		if strings.Contains(v.Match, matchPrefix) {
			count++
		}
	}
	require.Equal(t, count, 5)
}

func (suite *OvnClientTestSuite) test_newAcl() {
	t := suite.T()
	t.Parallel()

	ovnClient := suite.ovnClient
	pgName := "test-new-acl-pg"
	priority := "1000"
	match := "outport==@ovn.sg.test_create_acl_pg && ip"
	options := func(acl *ovnnb.ACL) {
		acl.Log = true
		acl.Severity = &ovnnb.ACLSeverityWarning
		acl.Name = &pgName
	}

	expect := &ovnnb.ACL{
		Name:      &pgName,
		Action:    ovnnb.ACLActionAllowRelated,
		Direction: ovnnb.ACLDirectionToLport,
		Match:     match,
		Priority:  1000,
		ExternalIDs: map[string]string{
			aclParentKey: pgName,
		},
		Log:      true,
		Severity: &ovnnb.ACLSeverityWarning,
	}

	acl, err := ovnClient.newAcl(pgName, ovnnb.ACLDirectionToLport, priority, match, ovnnb.ACLActionAllowRelated, options)
	require.NoError(t, err)
	expect.UUID = acl.UUID
	require.Equal(t, expect, acl)
}

func (suite *OvnClientTestSuite) testnewNetworkPolicyAclMatch() {
	t := suite.T()
	t.Parallel()

	pgName := "test-new-acl-m-pg"
	asAllowName := "test.default.xx.allow.ipv4"
	asExceptName := "test.default.xx.except.ipv4"

	t.Run("has ingress network policy port", func(t *testing.T) {
		t.Parallel()

		npp := mockNetworkPolicyPort()
		matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, npp, nil)
		require.ElementsMatch(t, []string{
			fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, npp[0].Port.IntVal),
			fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && %d <= tcp.dst <= %d", pgName, asAllowName, asExceptName, npp[1].Port.IntVal, *npp[1].EndPort),
		}, matches)
	})

	t.Run("has egress network policy port", func(t *testing.T) {
		t.Parallel()

		npp := mockNetworkPolicyPort()

		matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionFromLport, npp, nil)
		require.ElementsMatch(t, []string{
			fmt.Sprintf("inport == @%s && ip && ip4.dst == $%s && ip4.dst != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, npp[0].Port.IntVal),
			fmt.Sprintf("inport == @%s && ip && ip4.dst == $%s && ip4.dst != $%s && %d <= tcp.dst <= %d", pgName, asAllowName, asExceptName, npp[1].Port.IntVal, *npp[1].EndPort),
		}, matches)
	})

	t.Run("network policy port is nil", func(t *testing.T) {
		t.Parallel()

		matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, nil, nil)
		require.ElementsMatch(t, []string{
			fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s", pgName, asAllowName, asExceptName),
		}, matches)
	})

	t.Run("has network policy port but port is not set", func(t *testing.T) {
		t.Parallel()

		npp := mockNetworkPolicyPort()
		npp[1].Port = nil

		matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, npp, nil)
		require.ElementsMatch(t, []string{
			fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, npp[0].Port.IntVal),
			fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp", pgName, asAllowName, asExceptName),
		}, matches)
	})

	t.Run("has network policy port but endPort is not set", func(t *testing.T) {
		t.Parallel()

		t.Run("port type is Int", func(t *testing.T) {
			t.Parallel()
			npp := mockNetworkPolicyPort()
			npp[1].EndPort = nil

			matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, npp, nil)
			require.ElementsMatch(t, []string{
				fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, npp[0].Port.IntVal),
				fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, npp[1].Port.IntVal),
			}, matches)
		})

		t.Run("port type is String", func(t *testing.T) {
			t.Parallel()
			protocolTcp := v1.ProtocolTCP
			npp := []netv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "test-pod-port",
					},
					Protocol: &protocolTcp,
				},
			}

			namedPortMap := map[string]*util.NamedPortInfo{
				"test-pod-port": {
					PortId: 13455,
				},
			}
			matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, npp, namedPortMap)
			require.ElementsMatch(t, []string{
				fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, 13455),
			}, matches)
		})

		t.Run("port type is String and not find named port", func(t *testing.T) {
			t.Parallel()
			protocolTcp := v1.ProtocolTCP
			npp := []netv1.NetworkPolicyPort{
				{
					Port: &intstr.IntOrString{
						Type:   intstr.String,
						StrVal: "test-pod-port-x",
					},
					Protocol: &protocolTcp,
				},
			}

			namedPortMap := map[string]*util.NamedPortInfo{
				"test-pod-port": {
					PortId: 13455,
				},
			}
			matches := newNetworkPolicyAclMatch(pgName, asAllowName, asExceptName, kubeovnv1.ProtocolIPv4, ovnnb.ACLDirectionToLport, npp, namedPortMap)
			require.ElementsMatch(t, []string{
				fmt.Sprintf("outport == @%s && ip && ip4.src == $%s && ip4.src != $%s && tcp.dst == %d", pgName, asAllowName, asExceptName, 0),
			}, matches)
		})
	})
}

func (suite *OvnClientTestSuite) test_aclFilter() {
	t := suite.T()
	t.Parallel()

	pgName := "test-filter-acl-pg"

	acls := make([]*ovnnb.ACL, 0)

	t.Run("filter acl", func(t *testing.T) {
		t.Parallel()

		match := "outport == @ovn.sg.test_list_acl_pg && ip"
		// create two to-lport acl
		for i := 0; i < 2; i++ {
			acl := newAcl(pgName, ovnnb.ACLDirectionToLport, "9999", match, ovnnb.ACLActionAllowRelated)
			acls = append(acls, acl)
		}

		// create two to-lport acl without acl parent key
		for i := 0; i < 2; i++ {
			acl := newAcl(pgName, ovnnb.ACLDirectionToLport, "9999", match, ovnnb.ACLActionAllowRelated)
			acl.ExternalIDs = nil
			acls = append(acls, acl)
		}

		// create two from-lport acl
		for i := 0; i < 3; i++ {
			acl := newAcl(pgName, ovnnb.ACLDirectionFromLport, "9999", match, ovnnb.ACLActionAllowRelated)
			acls = append(acls, acl)
		}

		// create four from-lport acl with other acl parent key
		for i := 0; i < 4; i++ {
			acl := newAcl(pgName, ovnnb.ACLDirectionFromLport, "9999", match, ovnnb.ACLActionAllowRelated)
			acl.ExternalIDs[aclParentKey] = pgName + "-test"
			acls = append(acls, acl)
		}

		/* include all direction acl */
		filterFunc := aclFilter("", nil)
		count := 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 11)

		/* include all direction acl with external ids */
		filterFunc = aclFilter("", map[string]string{aclParentKey: pgName})
		count = 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 5)

		/* include to-lport acl */
		filterFunc = aclFilter(ovnnb.ACLDirectionToLport, nil)
		count = 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 4)

		/* include to-lport acl with external ids */
		filterFunc = aclFilter(ovnnb.ACLDirectionToLport, map[string]string{aclParentKey: pgName})
		count = 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 2)

		/* include from-lport acl */
		filterFunc = aclFilter(ovnnb.ACLDirectionFromLport, nil)
		count = 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 7)

		/* include all from-lport acl with acl parent key*/
		filterFunc = aclFilter(ovnnb.ACLDirectionFromLport, map[string]string{aclParentKey: ""})
		count = 0
		for _, acl := range acls {
			if filterFunc(acl) {
				count++
			}
		}
		require.Equal(t, count, 7)
	})

	t.Run("result should exclude acl when externalIDs's length is not equal", func(t *testing.T) {
		t.Parallel()

		match := "outport == @ovn.sg.test_filter_acl_pg && ip"
		acl := newAcl(pgName, ovnnb.ACLDirectionToLport, "9999", match, ovnnb.ACLActionAllowRelated)

		filterFunc := aclFilter("", map[string]string{
			aclParentKey: pgName,
			"key":        "value",
		})

		require.False(t, filterFunc(acl))
	})
}

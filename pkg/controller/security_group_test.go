package controller

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"

	"github.com/kubeovn/kube-ovn/pkg/ovs"
	"github.com/kubeovn/kube-ovn/pkg/ovsdb/ovnnb"
)

func mockLsp() *ovnnb.LogicalSwitchPort {
	return &ovnnb.LogicalSwitchPort{
		ExternalIDs: map[string]string{
			"associated_sg_default-securitygroup": "false",
			"associated_sg_sg":                    "true",
			"security_groups":                     "sg",
		},
	}
}

func Test_getPortSg(t *testing.T) {
	t.Run("only have one sg", func(t *testing.T) {
		c := &Controller{}
		port := mockLsp()
		out, err := c.getPortSg(port)
		require.NoError(t, err)
		require.Equal(t, []string{"sg"}, out)
	})

	t.Run("have two and more sgs", func(t *testing.T) {
		c := &Controller{}
		port := mockLsp()
		port.ExternalIDs["associated_sg_default-securitygroup"] = "true"
		out, err := c.getPortSg(port)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"sg", "default-securitygroup"}, out)
	})
}

func Test_securityGroupALLNotExist(t *testing.T) {
	t.Parallel()

	fakeController := newFakeController(t)
	ctrl := fakeController.fakeController
	mockOvnClient := fakeController.mockOvnClient

	sgName := "sg"
	pgName := ovs.GetSgPortGroupName(sgName)

	t.Run("should return false when some port group exist", func(t *testing.T) {
		mockOvnClient.EXPECT().PortGroupExists(pgName).Return(true, nil)

		allNotExist, err := ctrl.securityGroupALLNotExist([]string{sgName, "sg1", "sg2", "sg3"})
		require.NoError(t, err)
		require.False(t, allNotExist)
	})

	t.Run("should return true when all port group does't exist", func(t *testing.T) {
		mockOvnClient.EXPECT().PortGroupExists(gomock.Any()).Return(false, nil).Times(3)

		allNotExist, err := ctrl.securityGroupALLNotExist([]string{"sg1", "sg2", "sg3"})
		require.NoError(t, err)
		require.True(t, allNotExist)
	})

	t.Run("should return true when sgs is empty", func(t *testing.T) {
		allNotExist, err := ctrl.securityGroupALLNotExist([]string{})
		require.NoError(t, err)
		require.True(t, allNotExist)
	})
}

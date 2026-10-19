package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestDepartmentManagedVisibleUserIDsIncludesSubtree(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&DingTalkDepartment{}, &DingTalkUserDepartment{}, &DepartmentDataPermission{}))
	require.NoError(t, DB.Create(&DingTalkDepartment{DeptID: 10, Name: "AGI", Path: "AI应用中心-AGI部"}).Error)
	require.NoError(t, DB.Create(&DingTalkDepartment{DeptID: 11, ParentDeptID: 10, Name: "AIGC", Path: "AI应用中心-AGI部-AIGC应用组"}).Error)
	require.NoError(t, DB.Create(&DingTalkDepartment{DeptID: 20, Name: "Other", Path: "其他部门"}).Error)
	require.NoError(t, DB.Create(&DingTalkUserDepartment{UserID: 2, Username: "child-a", DeptID: 10, DeptPath: "AI应用中心-AGI部"}).Error)
	require.NoError(t, DB.Create(&DingTalkUserDepartment{UserID: 3, Username: "child-b", DeptID: 11, DeptPath: "AI应用中心-AGI部-AIGC应用组"}).Error)
	require.NoError(t, DB.Create(&DingTalkUserDepartment{UserID: 4, Username: "other", DeptID: 20, DeptPath: "其他部门"}).Error)
	require.NoError(t, DB.Create(&DepartmentDataPermission{UserID: 1, DeptID: 10, ScopeType: DepartmentScopeSubtree}).Error)

	ids, departments, err := DepartmentManagedVisibleUserIDs(1)
	require.NoError(t, err)
	require.Equal(t, []int{2, 3}, ids)
	require.Len(t, departments, 1)
	require.Equal(t, int64(10), departments[0].DeptID)
}

func TestDashboardDepartmentScopePilotAndRoot(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&DingTalkDepartment{}, &DingTalkUserDepartment{}, &DepartmentDataPermission{}))
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{
		DepartmentDashboardScopeEnabledOption:    "true",
		DepartmentDashboardScopePilotUsersOption: "[\"manager\"]",
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})

	rootScope, err := DashboardDepartmentScopeForUser(99, "root", common.RoleRootUser)
	require.NoError(t, err)
	require.False(t, rootScope.Restricted)
	require.Equal(t, "root_full_access", rootScope.Reason)

	outsideScope, err := DashboardDepartmentScopeForUser(2, "other-admin", common.RoleAdminUser)
	require.NoError(t, err)
	require.False(t, outsideScope.Restricted)
	require.Equal(t, "not_in_pilot", outsideScope.Reason)

	pilotScope, err := DashboardDepartmentScopeForUser(1, "manager", common.RoleAdminUser)
	require.NoError(t, err)
	require.True(t, pilotScope.Restricted)
	require.Equal(t, "no_department_permission", pilotScope.Reason)
	require.Empty(t, pilotScope.VisibleUserIDs)
}

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
	require.NoError(t, DB.Create(&Channel{Status: common.ChannelStatusEnabled, Group: "AI应用中心-AGI部"}).Error)

	ids, departments, err := DepartmentManagedVisibleUserIDs(1)
	require.NoError(t, err)
	require.Equal(t, []int{2, 3}, ids)
	require.Len(t, departments, 1)
	require.Equal(t, int64(10), departments[0].DeptID)
}

func setDashboardDepartmentScopeOptions(t *testing.T, enabled, pilots string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	common.OptionMap = map[string]string{
		DepartmentDashboardScopeEnabledOption:    enabled,
		DepartmentDashboardScopePilotUsersOption: pilots,
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.OptionMapRWMutex.Unlock()
	})
}

func TestDashboardDepartmentScopeAdminFullAccess(t *testing.T) {
	for _, permissions := range []string{"none", "eligible_department", "department_without_channel"} {
		t.Run(permissions, func(t *testing.T) {
			truncateTables(t)
			require.NoError(t, DB.AutoMigrate(&DingTalkDepartment{}, &DingTalkUserDepartment{}, &DepartmentDataPermission{}))
			if permissions != "none" {
				require.NoError(t, DB.Create(&DingTalkDepartment{DeptID: 10, Path: "Engineering"}).Error)
				require.NoError(t, DB.Create(&DingTalkUserDepartment{UserID: 2, DeptID: 10, DeptPath: "Engineering"}).Error)
				require.NoError(t, DB.Create(&DepartmentDataPermission{UserID: 1, DeptID: 10, ScopeType: DepartmentScopeSubtree}).Error)
			}
			if permissions == "eligible_department" {
				require.NoError(t, DB.Create(&Channel{Status: common.ChannelStatusEnabled, Group: "Engineering"}).Error)
			}
			for _, pilot := range []struct {
				name  string
				value string
			}{
				{"empty", ""},
				{"username_match", `["admin"]`},
				{"id_match", `["1"]`},
				{"outside_pilot", `["other-user"]`},
			} {
				t.Run(pilot.name, func(t *testing.T) {
					setDashboardDepartmentScopeOptions(t, "true", pilot.value)
					scope, err := DashboardDepartmentScopeForUser(1, "admin", common.RoleAdminUser)
					require.NoError(t, err)
					require.True(t, scope.Enabled)
					require.False(t, scope.Restricted)
					require.Equal(t, "admin_full_access", scope.Reason)
					require.Empty(t, scope.VisibleUserIDs)
					require.Empty(t, scope.ManagedDepartments)
				})
			}
		})
	}
}

func TestDashboardDepartmentScopePrivilegedUsersDoNotQueryDatabase(t *testing.T) {
	setDashboardDepartmentScopeOptions(t, "true", `["admin","root"]`)
	previousDB := DB
	DB = nil
	t.Cleanup(func() { DB = previousDB })
	for _, tc := range []struct {
		name   string
		role   int
		reason string
	}{
		{"root", common.RoleRootUser, "root_full_access"},
		{"admin", common.RoleAdminUser, "admin_full_access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				scope, err := DashboardDepartmentScopeForUser(1, tc.name, tc.role)
				require.NoError(t, err)
				require.False(t, scope.Restricted)
				require.Equal(t, tc.reason, scope.Reason)
				require.Empty(t, scope.VisibleUserIDs)
				require.Empty(t, scope.ManagedDepartments)
			})
		})
	}
}

func TestDashboardDepartmentScopeOrdinaryUsers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		permission bool
		channel    string
		status     int
		wantIDs    []int
		wantReason string
	}{
		{"manager_subtree", true, "Engineering", common.ChannelStatusEnabled, []int{2, 3}, "department_scope"},
		{"manager_without_channel", true, "", 0, []int{1}, "self"},
		{"manager_disabled_channel", true, "Engineering", common.ChannelStatusManuallyDisabled, []int{1}, "self"},
		{"manager_unrelated_channel", true, "Other", common.ChannelStatusEnabled, []int{1}, "self"},
		{"plain_user", false, "Engineering", common.ChannelStatusEnabled, []int{1}, "self"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			setDashboardDepartmentScopeOptions(t, "true", `["other-user"]`)
			require.NoError(t, DB.AutoMigrate(&DingTalkDepartment{}, &DingTalkUserDepartment{}, &DepartmentDataPermission{}))
			require.NoError(t, DB.Create(&[]DingTalkDepartment{
				{DeptID: 10, Path: "Engineering"},
				{DeptID: 11, ParentDeptID: 10, Path: "Engineering-Team"},
				{DeptID: 20, Path: "Other"},
			}).Error)
			require.NoError(t, DB.Create(&[]DingTalkUserDepartment{
				{UserID: 2, DeptID: 10, DeptPath: "Engineering"},
				{UserID: 3, DeptID: 11, DeptPath: "Engineering-Team"},
				{UserID: 4, DeptID: 20, DeptPath: "Other"},
			}).Error)
			if tc.permission {
				require.NoError(t, DB.Create(&DepartmentDataPermission{UserID: 1, DeptID: 10, ScopeType: DepartmentScopeSubtree}).Error)
			}
			if tc.channel != "" {
				require.NoError(t, DB.Create(&Channel{Status: tc.status, Group: tc.channel}).Error)
			}
			scope, err := DashboardDepartmentScopeForUser(1, "ordinary-user", common.RoleCommonUser)
			require.NoError(t, err)
			require.True(t, scope.Restricted)
			require.Equal(t, tc.wantReason, scope.Reason)
			require.Equal(t, tc.wantIDs, scope.VisibleUserIDs)
			if tc.wantReason == "department_scope" {
				require.Len(t, scope.ManagedDepartments, 1)
				require.Equal(t, int64(10), scope.ManagedDepartments[0].DeptID)
			} else {
				require.Empty(t, scope.ManagedDepartments)
			}
		})
	}
}

func TestDashboardDepartmentScopeDisabled(t *testing.T) {
	setDashboardDepartmentScopeOptions(t, "false", `["user"]`)
	previousDB := DB
	DB = nil
	t.Cleanup(func() { DB = previousDB })
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		scope, err := DashboardDepartmentScopeForUser(1, "user", role)
		require.NoError(t, err)
		require.False(t, scope.Enabled)
		require.False(t, scope.Restricted)
		require.Equal(t, "disabled", scope.Reason)
		require.Empty(t, scope.VisibleUserIDs)
		require.Empty(t, scope.ManagedDepartments)
	}
}

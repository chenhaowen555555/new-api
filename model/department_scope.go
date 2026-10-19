package model

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	DepartmentDashboardScopeEnabledOption    = "DepartmentDashboardScopeEnabled"
	DepartmentDashboardScopePilotUsersOption = "DepartmentDashboardScopePilotUsers"
	DepartmentScopeExact                     = "exact"
	DepartmentScopeSubtree                   = "subtree"
)

type DingTalkDepartment struct {
	DeptID         int64  `json:"dept_id" gorm:"primaryKey;column:dept_id"`
	ParentDeptID   int64  `json:"parent_dept_id" gorm:"index;column:parent_dept_id;default:0"`
	Name           string `json:"name" gorm:"type:varchar(191);not null;default:''"`
	Path           string `json:"path" gorm:"type:varchar(512);index;not null;default:''"`
	ManagerUserIDs string `json:"manager_userids" gorm:"type:text;column:manager_userids"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
}

type DingTalkUserDepartment struct {
	Id             int    `json:"id"`
	UserID         int    `json:"user_id" gorm:"index;column:user_id;default:0"`
	Username       string `json:"username" gorm:"type:varchar(64);index;not null;default:''"`
	DingTalkUserID string `json:"dingtalk_userid" gorm:"type:varchar(128);index;column:dingtalk_userid;not null;default:''"`
	DeptID         int64  `json:"dept_id" gorm:"index:idx_dt_user_dept,priority:2;column:dept_id;default:0"`
	DeptPath       string `json:"dept_path" gorm:"type:varchar(512);index;column:dept_path;not null;default:''"`
	IsPrimary      bool   `json:"is_primary" gorm:"index;column:is_primary;default:false"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
}

type DepartmentDataPermission struct {
	Id             int    `json:"id"`
	UserID         int    `json:"user_id" gorm:"index:idx_dept_perm_user_dept,priority:1;column:user_id;default:0"`
	DingTalkUserID string `json:"dingtalk_userid" gorm:"type:varchar(128);index;column:dingtalk_userid;not null;default:''"`
	DeptID         int64  `json:"dept_id" gorm:"index:idx_dept_perm_user_dept,priority:2;column:dept_id;default:0"`
	ScopeType      string `json:"scope_type" gorm:"type:varchar(16);index;column:scope_type;not null;default:'subtree'"`
	Source         string `json:"source" gorm:"type:varchar(32);column:source;not null;default:'dingtalk'"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime;column:updated_at"`
}

type DepartmentDashboardScope struct {
	Enabled            bool                 `json:"enabled"`
	PilotUsers         []string             `json:"pilot_users"`
	Restricted         bool                 `json:"restricted"`
	Reason             string               `json:"reason"`
	VisibleUserIDs     []int                `json:"visible_user_ids"`
	ManagedDepartments []DingTalkDepartment `json:"managed_departments"`
}

type DepartmentScopeSnapshot struct {
	Departments     []DingTalkDepartment       `json:"departments"`
	UserDepartments []DingTalkUserDepartment   `json:"user_departments"`
	Permissions     []DepartmentDataPermission `json:"permissions"`
}

func departmentScopeOption(key string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return strings.TrimSpace(common.OptionMap[key])
}

func DepartmentDashboardScopeEnabled() bool {
	raw := strings.ToLower(departmentScopeOption(DepartmentDashboardScopeEnabledOption))
	return raw == "true" || raw == "1" || raw == "yes" || raw == "on"
}

func DepartmentDashboardScopePilotUsers() []string {
	raw := departmentScopeOption(DepartmentDashboardScopePilotUsersOption)
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err == nil {
		return compactStrings(values)
	}
	return compactStrings(strings.Split(raw, ","))
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func pilotMatches(userID int, username string, pilotUsers []string) bool {
	if len(pilotUsers) == 0 {
		return true
	}
	idText := strconv.Itoa(userID)
	for _, pilot := range pilotUsers {
		if pilot == username || pilot == idText {
			return true
		}
	}
	return false
}

func DashboardDepartmentScopeForUser(userID int, username string, role int) (DepartmentDashboardScope, error) {
	scope := DepartmentDashboardScope{
		Enabled:    DepartmentDashboardScopeEnabled(),
		PilotUsers: DepartmentDashboardScopePilotUsers(),
		Reason:     "disabled",
	}
	if !scope.Enabled {
		return scope, nil
	}
	if role >= common.RoleRootUser {
		scope.Reason = "root_full_access"
		return scope, nil
	}
	if role < common.RoleAdminUser {
		scope.Restricted = true
		scope.Reason = "self"
		scope.VisibleUserIDs = []int{userID}
		return scope, nil
	}
	if !pilotMatches(userID, username, scope.PilotUsers) {
		scope.Reason = "not_in_pilot"
		return scope, nil
	}

	visibleUserIDs, departments, err := DepartmentManagedVisibleUserIDs(userID)
	if err != nil {
		return scope, err
	}
	scope.Restricted = true
	scope.VisibleUserIDs = visibleUserIDs
	scope.ManagedDepartments = departments
	if len(departments) == 0 {
		scope.Reason = "no_department_permission"
	} else {
		scope.Reason = "department_scope"
	}
	return scope, nil
}

func ReplaceDepartmentScopeSnapshot(snapshot DepartmentScopeSnapshot) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM department_data_permissions").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM ding_talk_user_departments").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM ding_talk_departments").Error; err != nil {
			return err
		}
		if len(snapshot.Departments) > 0 {
			if err := tx.CreateInBatches(snapshot.Departments, 200).Error; err != nil {
				return err
			}
		}
		if len(snapshot.UserDepartments) > 0 {
			if err := tx.CreateInBatches(snapshot.UserDepartments, 500).Error; err != nil {
				return err
			}
		}
		if len(snapshot.Permissions) > 0 {
			if err := tx.CreateInBatches(snapshot.Permissions, 200).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func DepartmentManagedVisibleUserIDs(managerUserID int) ([]int, []DingTalkDepartment, error) {
	var perms []DepartmentDataPermission
	if err := DB.Where("user_id = ?", managerUserID).Find(&perms).Error; err != nil {
		return nil, nil, err
	}
	if len(perms) == 0 {
		return []int{}, []DingTalkDepartment{}, nil
	}

	deptIDs := make([]int64, 0, len(perms))
	for _, perm := range perms {
		if perm.DeptID > 0 {
			deptIDs = append(deptIDs, perm.DeptID)
		}
	}
	var departments []DingTalkDepartment
	if len(deptIDs) > 0 {
		if err := DB.Where("dept_id IN ?", deptIDs).Find(&departments).Error; err != nil {
			return nil, nil, err
		}
	}
	deptByID := make(map[int64]DingTalkDepartment, len(departments))
	for _, dept := range departments {
		deptByID[dept.DeptID] = dept
	}

	visible := map[int]struct{}{}
	for _, perm := range perms {
		if perm.DeptID <= 0 {
			continue
		}
		query := DB.Model(&DingTalkUserDepartment{}).Where("user_id > 0")
		if strings.EqualFold(perm.ScopeType, DepartmentScopeExact) {
			query = query.Where("dept_id = ?", perm.DeptID)
		} else if dept, ok := deptByID[perm.DeptID]; ok && strings.TrimSpace(dept.Path) != "" {
			query = query.Where("dept_id = ? OR dept_path LIKE ?", perm.DeptID, dept.Path+"-%")
		} else {
			query = query.Where("dept_id = ?", perm.DeptID)
		}

		var rows []DingTalkUserDepartment
		if err := query.Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			if row.UserID > 0 {
				visible[row.UserID] = struct{}{}
			}
		}
	}

	ids := make([]int, 0, len(visible))
	for id := range visible {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	sort.Slice(departments, func(i, j int) bool { return departments[i].Path < departments[j].Path })
	return ids, departments, nil
}

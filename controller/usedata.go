package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func parseFlowQuotaTimeRange(c *gin.Context) (int64, int64, bool) {
	startTimestamp, err := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	if err != nil || startTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid start_timestamp")
		return 0, 0, false
	}
	endTimestamp, err := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if err != nil || endTimestamp <= 0 {
		common.ApiErrorMsg(c, "invalid end_timestamp")
		return 0, 0, false
	}
	if endTimestamp < startTimestamp {
		common.ApiErrorMsg(c, "invalid time range")
		return 0, 0, false
	}
	return startTimestamp, endTimestamp, true
}

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")
	scope, err := model.DashboardDepartmentScopeForUser(c.GetInt("id"), c.GetString("username"), c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetAllQuotaDatesWithScope(startTimestamp, endTimestamp, username, scope.VisibleUserIDs, scope.Restricted)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	scope, err := model.DashboardDepartmentScopeForUser(c.GetInt("id"), c.GetString("username"), c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	dates, err := model.GetQuotaDataGroupByUserWithScope(startTimestamp, endTimestamp, scope.VisibleUserIDs, scope.Restricted)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetUserQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 判断时间跨度是否超过 1 个月
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := model.GetQuotaDataByUserId(userId, startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetAllFlowQuotaDates(c *gin.Context) {
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	username := c.Query("username")
	role := c.GetInt("role")
	scope, err := model.DashboardDepartmentScopeForUser(c.GetInt("id"), c.GetString("username"), role)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	flowRole := role
	if flowRole < common.RoleAdminUser && scope.Reason == "department_scope" {
		flowRole = common.RoleAdminUser
	}
	dates, err := model.GetFlowQuotaDataWithScope(startTimestamp, endTimestamp, username, 0, flowRole, scope.VisibleUserIDs, scope.Restricted)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetUserFlowQuotaDates(c *gin.Context) {
	userId := c.GetInt("id")
	startTimestamp, endTimestamp, ok := parseFlowQuotaTimeRange(c)
	if !ok {
		return
	}
	if endTimestamp-startTimestamp > 2592000 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "时间跨度不能超过 1 个月",
		})
		return
	}
	dates, err := model.GetFlowQuotaData(startTimestamp, endTimestamp, "", userId, common.RoleCommonUser)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func SyncDepartmentDashboardScope(c *gin.Context) {
	var snapshot model.DepartmentScopeSnapshot
	if err := c.ShouldBindJSON(&snapshot); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.ReplaceDepartmentScopeSnapshot(snapshot); err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"departments":      len(snapshot.Departments),
			"user_departments": len(snapshot.UserDepartments),
			"permissions":      len(snapshot.Permissions),
		},
	})
}

func GetDepartmentDashboardScopeDebug(c *gin.Context) {
	scope, err := model.DashboardDepartmentScopeForUser(c.GetInt("id"), c.GetString("username"), c.GetInt("role"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	type debugUser struct {
		Id          int    `json:"id"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Role        int    `json:"role"`
		Group       string `json:"group"`
	}
	users := make([]debugUser, 0)
	if len(scope.VisibleUserIDs) > 0 {
		if err := model.DB.Model(&model.User{}).Select("id, username, display_name, role, \"group\"").Where("id IN ?", scope.VisibleUserIDs).Order("id ASC").Find(&users).Error; err != nil {
			common.ApiError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"current_user": gin.H{
				"id":       c.GetInt("id"),
				"username": c.GetString("username"),
				"role":     c.GetInt("role"),
			},
			"scope":              scope,
			"visible_user_count": len(scope.VisibleUserIDs),
			"visible_users":      users,
		},
	})
}

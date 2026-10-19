package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

type casdoorUserForGrouping struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Properties  map[string]string `json:"properties"`
}

type dingTalkDeptForGrouping struct {
	Name     string `json:"name"`
	ParentID int64  `json:"parent_id"`
}

type dingTalkUserForGrouping struct {
	Name          string           `json:"name"`
	DeptIDs       []int64          `json:"dept_id_list"`
	DeptOrderList []map[string]any `json:"dept_order_list"`
}

func syncOAuthDingTalkUserGroup(user *model.User, providerUserID string) {
	if user == nil || user.Id == 0 || !strings.HasPrefix(user.Username, "oidc_") {
		return
	}
	if os.Getenv("DINGTALK_APP_KEY") == "" || os.Getenv("DINGTALK_APP_SECRET") == "" || os.Getenv("CASDOOR_CLIENT_ID") == "" || os.Getenv("CASDOOR_CLIENT_SECRET") == "" {
		common.SysLog("[OAuth-DingTalk-Group] skipped: missing env")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	casdoorUser, err := findCasdoorUserForGrouping(ctx, user.Username, providerUserID)
	if err != nil {
		common.SysLog("[OAuth-DingTalk-Group] casdoor lookup failed: " + err.Error())
		return
	}
	props := casdoorUser.Properties
	unionID := strings.TrimSpace(props["oauth_DingTalk_unionId"])
	if unionID == "" {
		unionID = strings.TrimSpace(casdoorUser.Name)
	}
	dingUserID := strings.TrimSpace(props["dingtalk_userid"])
	if dingUserID == "" {
		dingUserID = strings.TrimSpace(props["DingTalk_userid"])
	}
	ding := &dingTalkGroupingClient{}
	if dingUserID == "" && unionID != "" {
		dingUserID, err = ding.useridByUnionID(ctx, unionID)
		if err != nil {
			common.SysLog("[OAuth-DingTalk-Group] unionid to userid failed: " + err.Error())
			return
		}
	}
	if dingUserID == "" {
		common.SysLog("[OAuth-DingTalk-Group] skipped: empty dingtalk userid")
		return
	}
	dingUser, err := ding.user(ctx, dingUserID)
	if err != nil {
		common.SysLog("[OAuth-DingTalk-Group] get user failed: " + err.Error())
		return
	}
	mainDeptID := primaryDeptIDForGrouping(dingUser)
	if mainDeptID == 0 {
		common.SysLog("[OAuth-DingTalk-Group] skipped: no department")
		return
	}
	leafPath, err := ding.departmentPath(ctx, mainDeptID)
	if err != nil || leafPath == "" {
		common.SysLog("[OAuth-DingTalk-Group] dept path failed: " + fmt.Sprint(err))
		return
	}
	targetGroup := nearestChannelBackedGroupForPath(leafPath)
	if targetGroup == "" {
		targetGroup = os.Getenv("NO_DEPT_GROUP")
		if targetGroup == "" {
			targetGroup = "no_dept"
		}
	}
	updates := map[string]any{}
	if targetGroup != "" && user.Group != targetGroup {
		updates["group"] = targetGroup
	}
	if dingUser.Name != "" && user.DisplayName != dingUser.Name {
		updates["display_name"] = dingUser.Name
	}
	if len(updates) == 0 {
		return
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Updates(updates).Error; err != nil {
		common.SysLog("[OAuth-DingTalk-Group] update user failed: " + err.Error())
		return
	}
	if groupValue, ok := updates["group"].(string); ok && groupValue != "" {
		_ = model.DB.Table("tokens").Where("user_id = ? AND deleted_at IS NULL", user.Id).Update("group", groupValue).Error
		user.Group = groupValue
	}
	if displayName, ok := updates["display_name"].(string); ok {
		user.DisplayName = displayName
	}
	common.SysLog(fmt.Sprintf("[OAuth-DingTalk-Group] user %s -> group %s (leaf %s)", user.Username, user.Group, leafPath))
}

func findCasdoorUserForGrouping(ctx context.Context, username, providerUserID string) (*casdoorUserForGrouping, error) {
	endpoint := strings.TrimRight(os.Getenv("CASDOOR_ENDPOINT"), "/")
	if endpoint == "" {
		endpoint = "https://agi-casdoor.bluetti.com"
	}
	org := os.Getenv("CASDOOR_ORG")
	if org == "" {
		org = "organization_poweroak"
	}
	url := fmt.Sprintf("%s/api/get-users?owner=%s&clientId=%s&clientSecret=%s", endpoint, org, os.Getenv("CASDOOR_CLIENT_ID"), os.Getenv("CASDOOR_CLIENT_SECRET"))
	var body struct {
		Status string                   `json:"status"`
		Data   []casdoorUserForGrouping `json:"data"`
	}
	if err := httpJSON(ctx, http.MethodGet, url, nil, &body); err != nil {
		return nil, err
	}
	for i := range body.Data {
		u := &body.Data[i]
		if u.ID == providerUserID || u.Name == username || u.Name == providerUserID || u.Properties["oauth_DingTalk_id"] == providerUserID {
			return u, nil
		}
	}
	return nil, fmt.Errorf("casdoor user not found: %s/%s", username, providerUserID)
}

type dingTalkGroupingClient struct{ token string; expiresAt time.Time }

func (d *dingTalkGroupingClient) accessToken(ctx context.Context) (string, error) {
	if d.token != "" && time.Now().Before(d.expiresAt) { return d.token, nil }
	url := fmt.Sprintf("https://oapi.dingtalk.com/gettoken?appkey=%s&appsecret=%s", os.Getenv("DINGTALK_APP_KEY"), os.Getenv("DINGTALK_APP_SECRET"))
	var body struct { ErrCode int `json:"errcode"`; ErrMsg string `json:"errmsg"`; AccessToken string `json:"access_token"` }
	if err := httpJSON(ctx, http.MethodGet, url, nil, &body); err != nil { return "", err }
	if body.ErrCode != 0 { return "", fmt.Errorf("gettoken: %d %s", body.ErrCode, body.ErrMsg) }
	d.token = body.AccessToken; d.expiresAt = time.Now().Add(7000*time.Second)
	return d.token, nil
}

func (d *dingTalkGroupingClient) post(ctx context.Context, path string, payload any, result any) error {
	token, err := d.accessToken(ctx); if err != nil { return err }
	url := fmt.Sprintf("https://oapi.dingtalk.com/%s?access_token=%s", path, token)
	var body struct { ErrCode int `json:"errcode"`; ErrMsg string `json:"errmsg"`; Result json.RawMessage `json:"result"` }
	if err := httpJSON(ctx, http.MethodPost, url, payload, &body); err != nil { return err }
	if body.ErrCode != 0 { return fmt.Errorf("%s: %d %s", path, body.ErrCode, body.ErrMsg) }
	if result != nil { return json.Unmarshal(body.Result, result) }
	return nil
}

func (d *dingTalkGroupingClient) useridByUnionID(ctx context.Context, unionID string) (string, error) {
	var result struct{ UserID string `json:"userid"` }
	err := d.post(ctx, "topapi/user/getbyunionid", map[string]string{"unionid": unionID}, &result)
	return result.UserID, err
}

func (d *dingTalkGroupingClient) user(ctx context.Context, userID string) (dingTalkUserForGrouping, error) {
	var result dingTalkUserForGrouping
	err := d.post(ctx, "topapi/v2/user/get", map[string]string{"userid": userID}, &result)
	return result, err
}

func (d *dingTalkGroupingClient) departmentPath(ctx context.Context, deptID int64) (string, error) {
	parts := []string{}
	current := deptID
	for i := 0; current > 0 && i < 20; i++ {
		var dept dingTalkDeptForGrouping
		if err := d.post(ctx, "topapi/v2/department/get", map[string]int64{"dept_id": current}, &dept); err != nil { break }
		if dept.Name != "" { parts = append([]string{dept.Name}, parts...) }
		current = dept.ParentID
	}
	if len(parts) > 1 && parts[0] == "深圳市德兰明海新能源股份有限公司" { parts = parts[1:] }
	return strings.Join(parts, "-"), nil
}

func primaryDeptIDForGrouping(user dingTalkUserForGrouping) int64 {
	if len(user.DeptOrderList) > 0 {
		var best int64
		var bestOrder float64
		for i, item := range user.DeptOrderList {
			deptID, _ := item["dept_id"].(float64)
			order, _ := item["order"].(float64)
			if i == 0 || order < bestOrder { best, bestOrder = int64(deptID), order }
		}
		if best > 0 { return best }
	}
	if len(user.DeptIDs) > 0 { return user.DeptIDs[0] }
	return 0
}

func nearestChannelBackedGroupForPath(path string) string {
	for current := path; current != ""; {
		if groupHasEnabledChannelForGrouping(current) { return current }
		idx := strings.LastIndex(current, "-")
		if idx < 0 { break }
		current = current[:idx]
	}
	return ""
}

func groupHasEnabledChannelForGrouping(group string) bool {
	var channelGroups []string
	if err := model.DB.Table("channels").Where("status = 1").Pluck("group", &channelGroups).Error; err != nil { return false }
	for _, raw := range channelGroups { for _, g := range strings.Split(raw, ",") { if strings.TrimSpace(g) == group { return true } } }
	return false
}

func httpJSON(ctx context.Context, method, url string, payload any, out any) error {
	var body *bytes.Reader
	if payload != nil { data, _ := json.Marshal(payload); body = bytes.NewReader(data) } else { body = bytes.NewReader(nil) }
	req, err := http.NewRequestWithContext(ctx, method, url, body); if err != nil { return err }
	if payload != nil { req.Header.Set("Content-Type", "application/json") }
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req); if err != nil { return err }
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 { return fmt.Errorf("%s returned %s", url, res.Status) }
	return json.NewDecoder(res.Body).Decode(out)
}

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ---------- 请求类型判定 ----------

func TestIsJSONAndFormRequest(t *testing.T) {
	jsonReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
	jsonReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	if !isJSONRequest(jsonReq) {
		t.Fatalf("应识别为 JSON 请求")
	}
	if isFormRequest(jsonReq) {
		t.Fatalf("JSON 请求不应识别为表单请求")
	}

	formReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=1"))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !isFormRequest(formReq) {
		t.Fatalf("应识别为表单请求")
	}
	if isJSONRequest(formReq) {
		t.Fatalf("表单请求不应识别为 JSON 请求")
	}

	// 没有 Content-Type 时按表单处理。
	plain := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=1"))
	if isJSONRequest(plain) {
		t.Fatalf("无 Content-Type 不应识别为 JSON 请求")
	}
}

// ---------- 表单字段映射 ----------

type formTarget struct {
	Name    string         `json:"name"`
	Count   int            `json:"count"`
	Ratio   float64        `json:"ratio"`
	Flag    bool           `json:"flag"`
	Size    uint           `json:"size"`
	Params  map[string]any `json:"params"`
	Skipped string
	hidden  string
}

func TestAssignFormMapsFields(t *testing.T) {
	var target formTarget
	values := url.Values{
		"name":   {"应用一"},
		"count":  {"12"},
		"ratio":  {"1.5"},
		"flag":   {"true"},
		"size":   {"7"},
		"params": {`{"k":"v"}`},
		// 未知字段应被忽略。
		"unknown": {"x"},
		// 无 json tag 的导出字段按字段名映射。
		"Skipped": {"映射"},
	}
	if err := assignForm(values, &target); err != nil {
		t.Fatalf("表单映射失败: %v", err)
	}
	if target.Name != "应用一" {
		t.Fatalf("Name = %s", target.Name)
	}
	if target.Count != 12 {
		t.Fatalf("Count = %d", target.Count)
	}
	if target.Ratio != 1.5 {
		t.Fatalf("Ratio = %v", target.Ratio)
	}
	if !target.Flag {
		t.Fatalf("Flag 应为 true")
	}
	if target.Size != 7 {
		t.Fatalf("Size = %d", target.Size)
	}
	if target.Params["k"] != "v" {
		t.Fatalf("Params = %+v", target.Params)
	}
	if target.Skipped != "映射" {
		t.Fatalf("无 json tag 字段应按字段名映射，实际 %q", target.Skipped)
	}
}

func TestAssignFormRejectsInvalidValues(t *testing.T) {
	cases := []url.Values{
		{"count": {"不是数字"}},
		{"ratio": {"不是浮点"}},
		{"flag": {"不是布尔"}},
		{"size": {"-1"}},
		{"params": {"不是 JSON"}},
	}
	for _, values := range cases {
		var target formTarget
		if err := assignForm(values, &target); err == nil {
			t.Fatalf("非法值 %v 应报错", values)
		}
	}
}

func TestAssignFormRejectsBadTarget(t *testing.T) {
	if err := assignForm(url.Values{}, formTarget{}); err == nil {
		t.Fatalf("非指针目标应报错")
	}
	if err := assignForm(url.Values{}, &[]string{}); err == nil {
		t.Fatalf("非结构体指针目标应报错")
	}
	var nilPtr *formTarget
	if err := assignForm(url.Values{}, nilPtr); err == nil {
		t.Fatalf("空指针目标应报错")
	}
}

func TestFormFieldName(t *testing.T) {
	type withTag struct {
		A string `json:"a"`
		B string `json:"b,omitempty"`
		C string `json:"-"`
		D string
	}
	// 通过 assignForm 的间接路径验证命名规则。
	var target withTag
	values := url.Values{"a": {"1"}, "b": {"2"}, "c": {"3"}, "D": {"4"}}
	if err := assignForm(values, &target); err != nil {
		t.Fatalf("表单映射失败: %v", err)
	}
	if target.A != "1" || target.B != "2" {
		t.Fatalf("json tag 映射失败: %+v", target)
	}
	if target.C != "" {
		t.Fatalf("json:\"-\" 的字段不应被映射")
	}
	if target.D != "4" {
		t.Fatalf("无 tag 字段应按字段名映射")
	}
}

func TestSetFormValueUnsupportedType(t *testing.T) {
	type withSlice struct {
		Items []string `json:"items"`
	}
	var target withSlice
	err := assignForm(url.Values{"items": {"x"}}, &target)
	if err == nil {
		t.Fatalf("不支持的类型应报错")
	}
}

func TestSetFormValueEmptyMap(t *testing.T) {
	var target formTarget
	if err := assignForm(url.Values{"params": {""}}, &target); err != nil {
		t.Fatalf("空 map 值应视为清空: %v", err)
	}
	if target.Params != nil {
		t.Fatalf("空 map 值应把字段置零: %+v", target.Params)
	}
}

func TestDecodeFormReadsBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("name=abc&count=3"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var target formTarget
	if err := decodeInput(req, &target); err != nil {
		t.Fatalf("表单解码失败: %v", err)
	}
	if target.Name != "abc" || target.Count != 3 {
		t.Fatalf("解码结果不符: %+v", target)
	}
}

func TestDecodeInputRejectsBrokenForm(t *testing.T) {
	// body 声明为表单但内容非法。
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("%zz=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var target formTarget
	if err := decodeInput(req, &target); err == nil {
		t.Fatalf("非法表单应报错")
	}
}

// ---------- 统一输出 ----------

func TestRespondRedirectsForFormRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.respond(rec, req, http.StatusOK, map[string]any{"ok": true}, "/ops", "操作成功")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/ops?") {
		t.Fatalf("重定向地址 = %s", loc)
	}
	if !strings.Contains(loc, "message=") {
		t.Fatalf("重定向地址应带成功提示: %s", loc)
	}
}

func TestRespondMarksClientErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.respond(rec, req, http.StatusBadRequest, map[string]any{"error": "x"}, "/ops", "原因缺失")
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error=") {
		t.Fatalf("4xx 应带错误提示: %s", loc)
	}
}

func TestRespondMasksInternalErrors(t *testing.T) {
	srv, _ := newTestServer(t)

	// JSON 请求。
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.respond(rec, req, http.StatusInternalServerError, map[string]any{"error": "数据库内部细节"}, "", "数据库内部细节")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "数据库内部细节") {
		t.Fatalf("内部错误细节不应透出: %s", rec.Body.String())
	}

	// 表单请求。
	formReq := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=1"))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.respond(rec, formReq, http.StatusInternalServerError, nil, "/ops", "数据库内部细节")
	loc := rec.Header().Get("Location")
	if strings.Contains(loc, "数据库内部细节") {
		t.Fatalf("内部错误细节不应透出到重定向参数: %s", loc)
	}
}

func TestRespondWritesJSONForJSONRequests(t *testing.T) {
	srv, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.respond(rec, req, http.StatusOK, map[string]any{"ok": true}, "/ops", "成功")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "\"ok\":true") {
		t.Fatalf("应返回 JSON: %s", rec.Body.String())
	}
}

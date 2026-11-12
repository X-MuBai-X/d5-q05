package httpapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// 本文件解决「HTML 表单提交」与「JSON 接口」共存的问题。
//
// 同一组接口既要被 curl / 脚本以 JSON 调用（题面共同要求里的接口自测），
// 也要能被运营工作台、租户管理台、应用调用台上的 HTML 表单直接操作，
// 因此这里按 Content-Type 分流：
//
//   - application/json                      → 严格 JSON 解码（拒绝未知字段）
//   - application/x-www-form-urlencoded     → 表单解码（按 json tag 映射字段）
//
// 表单解码会做类型转换（字符串 → 整数/布尔），因此页面上的
// <input type="number"> 也能正确落到 int 字段。

// isJSONRequest 判断请求是否为 JSON 请求。
func isJSONRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "application/json")
}

// isFormRequest 判断请求是否来自 HTML 表单。
func isFormRequest(r *http.Request) bool {
	return !isJSONRequest(r) &&
		strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")
}

// decodeInput 按请求类型解析输入到 target。
func decodeInput(r *http.Request, target any) error {
	if isJSONRequest(r) {
		return decodeJSON(r, target)
	}
	return decodeForm(r, target)
}

// decodeForm 解析 HTML 表单提交并按 json tag 填充结构体字段。
func decodeForm(r *http.Request, target any) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("表单解析失败: %w", err)
	}
	return assignForm(r.Form, target)
}

// assignForm 把表单值写入结构体字段。
//
// 规则：
//   - 字段名取 json tag 的第一段；没有 json tag 时取字段名本身；
//   - 表单中未出现的字段保持零值（不覆盖 JSON 语义里的「未传」）；
//   - 表单中出现的未知字段直接忽略（例如登录页的 kind 隐藏域）。
func assignForm(values url.Values, target any) error {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("表单目标必须是非空结构体指针")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("表单目标必须是结构体指针")
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue // 未导出字段
		}
		name := formFieldName(field)
		if name == "" || name == "-" {
			continue
		}
		raw, ok := values[name]
		if !ok || len(raw) == 0 {
			continue
		}
		if err := setFormValue(rv.Field(i), raw[0]); err != nil {
			return fmt.Errorf("表单字段 %q 无效: %w", name, err)
		}
	}
	return nil
}

// formFieldName 取得字段对应的表单名。
func formFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name
	}
	return name
}

// setFormValue 按字段类型转换表单值。
func setFormValue(f reflect.Value, raw string) error {
	switch f.Kind() {
	case reflect.String:
		f.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return fmt.Errorf("需要整数，收到 %q", raw)
		}
		f.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return fmt.Errorf("需要非负整数，收到 %q", raw)
		}
		f.SetUint(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("需要布尔值，收到 %q", raw)
		}
		f.SetBool(b)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return fmt.Errorf("需要数字，收到 %q", raw)
		}
		f.SetFloat(n)
	case reflect.Map:
		// map 字段只接受 JSON 编码的字符串值。
		if strings.TrimSpace(raw) == "" {
			f.Set(reflect.Zero(f.Type()))
			return nil
		}
		// 必须解码到 map 的指针，否则 json 会报「non-pointer map」。
		ptr := reflect.New(f.Type())
		if err := json.Unmarshal([]byte(raw), ptr.Interface()); err != nil {
			return fmt.Errorf("需要 JSON 对象，收到 %q", raw)
		}
		f.Set(ptr.Elem())
	default:
		return fmt.Errorf("不支持的表单字段类型 %s", f.Kind())
	}
	return nil
}

// respond 统一输出。
//
// JSON 请求返回 JSON；HTML 表单请求则重定向回对应工作台并附带提示，
// 这样页面上的表单可以真正操作而不必手工解析 JSON。
//
// 500 类内部错误不把细节透出给调用方（既不回 JSON，也不放进重定向参数）。
func (s *Server) respond(w http.ResponseWriter, r *http.Request, status int, payload any, redirectTo, message string) {
	if status >= http.StatusInternalServerError {
		log.Printf("服务端内部错误: %s", message)
		if isFormRequest(r) && redirectTo != "" {
			http.Redirect(w, r, redirectTo+"?error="+url.QueryEscape("服务内部错误，请稍后重试"),
				http.StatusSeeOther)
			return
		}
		writeJSON(w, status, map[string]any{"error": "服务内部错误，请稍后重试"})
		return
	}
	if isFormRequest(r) && redirectTo != "" {
		q := url.Values{}
		if message != "" {
			if status >= http.StatusBadRequest {
				// 4xx 属于用户可纠正的错误，用红色提示区展示。
				q.Set("error", message)
			} else {
				q.Set("message", message)
			}
		}
		target := redirectTo
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	writeJSON(w, status, payload)
}

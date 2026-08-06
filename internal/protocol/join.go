package protocol

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

var joinCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{40,128}$`)

func ParseJoinDescriptor(raw string) (JoinDescriptorV1, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return JoinDescriptorV1{}, fmt.Errorf("解析连接信息失败: %w", err)
	}
	if u.Scheme != "homestack" || u.Host != "join" || (u.Path != "" && u.Path != "/") {
		return JoinDescriptorV1{}, errors.New("连接信息必须使用 homestack://join 格式")
	}
	if u.User != nil || u.Fragment != "" {
		return JoinDescriptorV1{}, errors.New("连接信息不能包含用户信息或片段")
	}
	query := u.Query()
	if len(query) != 2 || len(query["server"]) != 1 || len(query["code"]) != 1 {
		return JoinDescriptorV1{}, errors.New("连接信息只能包含单个 server 和 code 参数")
	}
	server, err := validateServerURL(query.Get("server"))
	if err != nil {
		return JoinDescriptorV1{}, err
	}
	code := query.Get("code")
	if !joinCodePattern.MatchString(code) {
		return JoinDescriptorV1{}, errors.New("一次性连接码格式无效")
	}
	return JoinDescriptorV1{Version: JoinVersion, Server: server, Code: code}, nil
}

func NewJoinDescriptor(server, code string) (JoinDescriptorV1, error) {
	server, err := validateServerURL(server)
	if err != nil {
		return JoinDescriptorV1{}, err
	}
	if !joinCodePattern.MatchString(code) {
		return JoinDescriptorV1{}, errors.New("一次性连接码格式无效")
	}
	return JoinDescriptorV1{Version: JoinVersion, Server: server, Code: code}, nil
}

func (d JoinDescriptorV1) String() string {
	query := url.Values{}
	query.Set("server", d.Server)
	query.Set("code", d.Code)
	return (&url.URL{Scheme: "homestack", Host: "join", RawQuery: query.Encode()}).String()
}

func validateServerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("解析控制服务器地址失败: %w", err)
	}
	if u.Scheme != "https" || u.Hostname() == "" {
		return "", errors.New("控制服务器必须是有效的 HTTPS 地址")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("控制服务器地址不能包含凭据、路径、查询或片段")
	}
	u.Path = ""
	return u.String(), nil
}

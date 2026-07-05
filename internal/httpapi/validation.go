package httpapi

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

type hostnameResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var publicURLResolver hostnameResolver = net.DefaultResolver

const supportedModel = "meitu-layering"

func ValidateCreateRequest(request CreateVideoRequest) (string, string, bool, error) {
	model := strings.TrimSpace(request.Model)
	if model == "" {
		return "", "", false, errors.New("model 是必填字段")
	}
	if model != supportedModel {
		return "", "", false, errors.New("model 不支持")
	}

	image := strings.TrimSpace(request.Image)
	if image == "" {
		return "", "", false, errors.New("image 是必填字段")
	}
	if err := validatePublicImageURL(image, publicURLResolver); err != nil {
		return "", "", false, err
	}

	subjectProtectFlag := false
	if request.SubjectProtectFlag != nil {
		subjectProtectFlag = *request.SubjectProtectFlag
	}
	return model, image, subjectProtectFlag, nil
}

func validatePublicImageURL(raw string, resolver hostnameResolver) error {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if strings.HasPrefix(lower, "data:") {
		return errors.New("image 不支持 data URL")
	}
	if strings.HasPrefix(lower, "file:") {
		return errors.New("image 不支持本地文件路径")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("image 必须是公网 http/https URL")
	}
	if parsed.User != nil {
		return errors.New("image URL 不允许包含用户名或密码")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("image 只支持 http/https URL")
	}

	host := parsed.Hostname()
	if host == "" {
		return errors.New("image URL 缺少 host")
	}
	ip := net.ParseIP(host)
	if ip == nil && !strings.Contains(host, ".") {
		return errors.New("image 不能指向明显的内网域名")
	}
	if isBlockedHostname(host) {
		return errors.New("image 不能指向 localhost、内网或链路本地地址")
	}
	if ip != nil && !isPublicIP(ip) {
		return errors.New("image 不能指向 localhost、内网或链路本地地址")
	}
	if ip == nil {
		if err := validateResolvedPublicIPs(host, resolver); err != nil {
			return err
		}
	}

	return nil
}

func validateResolvedPublicIPs(host string, resolver hostnameResolver) error {
	if resolver == nil {
		return errors.New("image URL 域名解析器未配置")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	addresses, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return errors.New("image URL 域名无法解析为公网地址")
	}
	for _, address := range addresses {
		if !isPublicIP(address.IP) {
			return errors.New("image 不能指向 localhost、内网或链路本地地址")
		}
	}
	return nil
}

func isBlockedHostname(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasSuffix(host, ".intranet") {
		return true
	}
	return false
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !ip.IsLoopback() &&
		!ip.IsPrivate() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified() &&
		!ip.IsMulticast()
}

package transporthttp

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// NewClientIP возвращает функцию определения адреса клиента. Заголовку X-Real-IP верим только если
// запрос пришёл от доверенного прокси из trusted (список CIDR), иначе берём адрес TCP-соединения.
// Иначе любой клиент мог бы подменить заголовок и обойти rate limit.
func NewClientIP(trusted []string) (func(*http.Request) string, error) {
	var nets []netip.Prefix
	for _, c := range trusted {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}

		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", c, err)
		}

		nets = append(nets, p)
	}

	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}

		peer, err := netip.ParseAddr(host)
		if err != nil {
			return host
		}

		for _, p := range nets {
			if p.Contains(peer.Unmap()) {
				if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
					return real.String()
				}
				break
			}
		}

		return peer.Unmap().String()
	}, nil
}

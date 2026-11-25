package authgate

import "net"

func netParse(cidr string) (net.IP, []*net.IPNet, error) {
	ip, n, err := net.ParseCIDR(cidr)
	return ip, []*net.IPNet{n}, err
}

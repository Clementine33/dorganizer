package bootstrap

import "fmt"

func BuildHandshakeLine(port int, token, version string, httpPort int) string {
	return fmt.Sprintf("ONSEI_BACKEND_READY port=%d token=%s version=%s http_port=%d", port, token, version, httpPort)
}

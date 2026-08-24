package bootstrap

import "fmt"

func BuildHandshakeLine(port int, token, version string) string {
	return fmt.Sprintf("ONSEI_BACKEND_READY port=%d token=%s version=%s", port, token, version)
}

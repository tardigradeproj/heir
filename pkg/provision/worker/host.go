package worker

import "os"

var hostname string

func init() {
	hostname, _ = os.Hostname()
}

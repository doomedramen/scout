package collector

import (
	"net"
	"os"
	"runtime"
	"time"
)

type Interface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}
type Snapshot struct {
	ObservedAt   time.Time   `json:"observedAt"`
	Hostname     string      `json:"hostname"`
	OS           string      `json:"os"`
	Architecture string      `json:"architecture"`
	CPUs         int         `json:"cpus"`
	Interfaces   []Interface `json:"interfaces"`
}

// Collect reads this machine only. It does not probe peers or install software.
func Collect() (Snapshot, error) {
	result := Snapshot{ObservedAt: time.Now().UTC(), OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU(), Interfaces: []Interface{}}
	var err error
	result.Hostname, err = os.Hostname()
	if err != nil {
		return result, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return result, err
	}
	for _, device := range interfaces {
		if device.Flags&net.FlagLoopback != 0 || device.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := device.Addrs()
		if err != nil {
			return result, err
		}
		item := Interface{Name: device.Name, Addresses: []string{}}
		for _, address := range addresses {
			item.Addresses = append(item.Addresses, address.String())
		}
		result.Interfaces = append(result.Interfaces, item)
	}
	return result, nil
}

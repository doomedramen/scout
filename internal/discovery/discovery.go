package discovery

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LocalInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

type Route struct {
	Interface string `json:"interface"`
	Prefix    string `json:"prefix"`
}

type Neighbor struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	State     string `json:"state"`
}

type LocalSnapshot struct {
	ObservedAt time.Time        `json:"observedAt"`
	Interfaces []LocalInterface `json:"interfaces"`
	Routes     []Route          `json:"routes"`
	Neighbors  []Neighbor       `json:"neighbors"`
}

// ObserveLocal reads Linux's non-invasive interface, route, and neighbor
// views. It never sends a packet and returns an empty snapshot when a proc
// view is unavailable, which keeps discovery useful in restricted hosts.
func ObserveLocal(ctx context.Context, root string) (LocalSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return LocalSnapshot{}, err
	}
	if root == "" {
		root = "/"
	}
	result := LocalSnapshot{ObservedAt: time.Now().UTC(), Interfaces: []LocalInterface{}, Routes: []Route{}, Neighbors: []Neighbor{}}
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, item := range interfaces {
			if item.Flags&net.FlagLoopback != 0 || item.Flags&net.FlagUp == 0 {
				continue
			}
			addresses, addressErr := item.Addrs()
			if addressErr != nil {
				continue
			}
			entry := LocalInterface{Name: item.Name, Addresses: []string{}}
			for _, address := range addresses {
				entry.Addresses = append(entry.Addresses, address.String())
			}
			result.Interfaces = append(result.Interfaces, entry)
		}
	}
	result.Routes = readRoutes(filepath.Join(root, "proc", "net", "route"))
	result.Neighbors = readNeighbors(filepath.Join(root, "proc", "net", "arp"))
	return result, nil
}

func readRoutes(path string) []Route {
	file, err := os.Open(path)
	if err != nil {
		return []Route{}
	}
	defer file.Close()
	result := []Route{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || fields[0] == "Iface" {
			continue
		}
		address, parseErr := littleEndianIPv4(fields[1])
		mask, maskErr := littleEndianIPv4(fields[2])
		if parseErr != nil || maskErr != nil {
			continue
		}
		prefix, prefixErr := netip.ParsePrefix(address + "/" + strconv.Itoa(maskBits(mask)))
		if prefixErr != nil {
			continue
		}
		result = append(result, Route{Interface: fields[0], Prefix: prefix.String()})
	}
	return result
}

func littleEndianIPv4(raw string) (string, error) {
	value, err := strconv.ParseUint(raw, 16, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(value), byte(value>>8), byte(value>>16), byte(value>>24)), nil
}

func maskBits(mask string) int {
	address, err := netip.ParseAddr(mask)
	if err != nil {
		return 0
	}
	bits := address.As4()
	result := 0
	for _, octet := range bits {
		result += bitsInByte(octet)
	}
	return result
}

func bitsInByte(value byte) int {
	result := 0
	for value != 0 {
		result++
		value &= value - 1
	}
	return result
}

func readNeighbors(path string) []Neighbor {
	file, err := os.Open(path)
	if err != nil {
		return []Neighbor{}
	}
	defer file.Close()
	result := []Neighbor{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || fields[0] == "IP" {
			continue
		}
		result = append(result, Neighbor{Interface: fields[5], Address: fields[0], State: fields[3]})
	}
	return result
}

type ProbePolicy struct {
	Ports           []int
	Concurrency     int
	ProbesPerSecond int
	TargetBudget    int
	Timeout         time.Duration
}

type ProbeResult struct {
	Address   string        `json:"address"`
	Port      int           `json:"port"`
	Reachable bool          `json:"reachable"`
	Latency   time.Duration `json:"latency"`
	Reason    string        `json:"reason,omitempty"`
}

func (p ProbePolicy) normalized() (ProbePolicy, error) {
	if p.Concurrency <= 0 {
		p.Concurrency = 16
	}
	if p.Concurrency > 16 {
		p.Concurrency = 16
	}
	if p.ProbesPerSecond <= 0 {
		p.ProbesPerSecond = 10
	}
	if p.ProbesPerSecond > 1000 {
		return ProbePolicy{}, errors.New("probe rate exceeds limit")
	}
	if p.TargetBudget <= 0 {
		p.TargetBudget = 256
	}
	if p.TargetBudget > 4096 {
		return ProbePolicy{}, errors.New("target budget exceeds limit")
	}
	if p.Timeout <= 0 {
		p.Timeout = 2 * time.Second
	}
	if p.Timeout > 10*time.Second {
		return ProbePolicy{}, errors.New("probe timeout exceeds limit")
	}
	for _, port := range p.Ports {
		if port < 1 || port > 65535 {
			return ProbePolicy{}, errors.New("invalid probe port")
		}
	}
	if len(p.Ports) == 0 {
		p.Ports = []int{22}
	}
	return p, nil
}

func Probe(ctx context.Context, addresses []string, policy ProbePolicy) ([]ProbeResult, error) {
	policy, err := policy.normalized()
	if err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(addresses))
	seen := map[string]bool{}
	for _, raw := range addresses {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(raw))
		if parseErr != nil || seen[address.String()] {
			continue
		}
		seen[address.String()] = true
		unique = append(unique, address.String())
		if len(unique) >= policy.TargetBudget {
			break
		}
	}
	jobs := make(chan struct {
		address string
		port    int
	})
	results := make(chan ProbeResult, len(unique)*len(policy.Ports))
	var group sync.WaitGroup
	for range policy.Concurrency {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				started := time.Now()
				dialer := net.Dialer{Timeout: policy.Timeout}
				connection, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(job.address, strconv.Itoa(job.port)))
				result := ProbeResult{Address: job.address, Port: job.port, Reachable: dialErr == nil, Latency: time.Since(started)}
				if dialErr != nil {
					result.Reason = "connection_failed"
				}
				if connection != nil {
					_ = connection.Close()
				}
				results <- result
			}
		}()
	}
	interval := time.Second / time.Duration(policy.ProbesPerSecond)
	last := time.Time{}
	for _, address := range unique {
		for _, port := range policy.Ports {
			if !last.IsZero() {
				timer := time.NewTimer(time.Until(last.Add(interval)))
				select {
				case <-ctx.Done():
					timer.Stop()
					close(jobs)
					group.Wait()
					close(results)
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			select {
			case jobs <- struct {
				address string
				port    int
			}{address: address, port: port}:
				last = time.Now()
			case <-ctx.Done():
				close(jobs)
				group.Wait()
				close(results)
				return nil, ctx.Err()
			}
		}
	}
	close(jobs)
	group.Wait()
	close(results)
	result := make([]ProbeResult, 0, len(results))
	for item := range results {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Address == result[j].Address {
			return result[i].Port < result[j].Port
		}
		return result[i].Address < result[j].Address
	})
	return result, nil
}

func ExpandTargets(ranges, exclusions []string, budget int) ([]string, error) {
	if budget <= 0 {
		budget = 256
	}
	if budget > 4096 {
		return nil, errors.New("target budget exceeds limit")
	}
	excludedAddresses := map[netip.Addr]bool{}
	excludedPrefixes := []netip.Prefix{}
	for _, raw := range exclusions {
		if address, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
			excludedAddresses[address] = true
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, errors.New("invalid exclusion")
		}
		excludedPrefixes = append(excludedPrefixes, prefix)
	}
	result := []string{}
	seen := map[netip.Addr]bool{}
	for _, raw := range ranges {
		address, addressErr := netip.ParseAddr(strings.TrimSpace(raw))
		if addressErr == nil {
			if !excludedAddresses[address] && !containsPrefix(excludedPrefixes, address) && !seen[address] {
				seen[address] = true
				result = append(result, address.String())
			}
			if len(result) >= budget {
				break
			}
			continue
		}
		prefix, prefixErr := netip.ParsePrefix(strings.TrimSpace(raw))
		if prefixErr != nil {
			return nil, errors.New("invalid target range")
		}
		current := prefix.Addr()
		for prefix.Contains(current) && len(result) < budget {
			if !excludedAddresses[current] && !containsPrefix(excludedPrefixes, current) && !seen[current] {
				seen[current] = true
				result = append(result, current.String())
			}
			next := current.Next()
			if !next.IsValid() || next == current {
				break
			}
			current = next
		}
		if len(result) >= budget {
			break
		}
	}
	sort.Strings(result)
	return result, nil
}

func containsPrefix(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func DecodeNeighborMAC(raw string) string {
	decoded, err := hex.DecodeString(strings.ReplaceAll(raw, ":", ""))
	if err != nil || len(decoded) != 6 {
		return ""
	}
	parts := make([]string, len(decoded))
	for index, value := range decoded {
		parts[index] = fmt.Sprintf("%02x", value)
	}
	return strings.Join(parts, ":")
}

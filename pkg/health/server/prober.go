// Copyright 2017 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"net"
	"strings"
	"time"

	"github.com/cilium/cilium/api/v1/health/models"
	ciliumModels "github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/logfields"

	"github.com/servak/go-fastping"
	"github.com/sirupsen/logrus"
)

type prober struct {
	*fastping.Pinger
	server *Server

	// 'stop' is closed upon a call to prober.Stop(). When the stopping is
	// finished, then prober.Done() will be notified.
	stop chan bool
	done chan bool

	// The lock protects multiple requests attempting to update the status
	// at the same time - ie, serialize updates between the periodic prober
	// and probes initiated via "GET /status/probe". It is also used to
	// co-ordinate updates of the ICMP responses and the HTTP responses.
	lock.RWMutex
	results map[ipString]*models.PathStatus
	nodes   nodeMap

	// TODO: If nodes leave the cluster, we will never clear out their
	//       entries in the 'results' map.
}

// copyResultRLocked makes a copy of the path status for the specified IP.
func (p *prober) copyResultRLocked(ip string) *models.PathStatus {
	status := p.results[ipString(ip)]
	if status == nil {
		return nil
	}

	result := &models.PathStatus{
		IP: ip,
	}
	paths := map[**models.ConnectivityStatus]*models.ConnectivityStatus{
		&result.Icmp:             status.Icmp,
		&result.HTTP:             status.HTTP,
		&result.HTTPViaL7:        status.HTTPViaL7,
		&result.HTTPViaService:   status.HTTPViaService,
		&result.HTTPViaServiceL7: status.HTTPViaServiceL7,
	}
	for res, value := range paths {
		if value != nil {
			*res = &*value
		}
	}
	return result
}

func getPrimaryIP(node *ciliumModels.NodeElement) string {
	if node.PrimaryAddress.IPV4.Enabled {
		return node.PrimaryAddress.IPV4.IP
	}
	return node.PrimaryAddress.IPV6.IP
}

// getResults gathers a copy of all of the results for nodes currently in the
// cluster.
func (p *prober) getResults() []*models.NodeStatus {
	p.RLock()
	defer p.RUnlock()

	// De-duplicate IPs in 'p.nodes' by building a map based on node.Name.
	resultMap := map[string]*models.NodeStatus{}
	for _, node := range p.nodes {
		if resultMap[node.Name] != nil {
			continue
		}
		primaryIP := getPrimaryIP(node)
		status := &models.NodeStatus{
			Name: node.Name,
			Host: &models.HostStatus{
				PrimaryAddress: p.copyResultRLocked(primaryIP),
			},
			// TODO: Endpoint: &models.PathStatus{},
		}
		secondaryResults := []*models.PathStatus{}
		for _, addr := range node.SecondaryAddresses {
			if addr.Enabled {
				secondaryStatus := p.copyResultRLocked(addr.IP)
				secondaryResults = append(secondaryResults, secondaryStatus)
			}
		}
		status.Host.SecondaryAddresses = secondaryResults
		resultMap[node.Name] = status
	}

	result := []*models.NodeStatus{}
	for _, res := range resultMap {
		result = append(result, res)
	}
	return result
}

func (p *prober) getNodes() nodeMap {
	p.RLock()
	defer p.RUnlock()
	return p.nodes
}

func isIPv4(ip string) bool {
	netIP := net.ParseIP(ip)
	return netIP != nil && !strings.Contains(ip, ":")
}

func skipAddress(elem *ciliumModels.NodeAddressingElement) bool {
	return elem == nil || !elem.Enabled || elem.IP == "<nil>"
}

// getAddresses returns a map of the node's addresses -> "primary" bool
func getNodeAddresses(node *ciliumModels.NodeElement) map[*ciliumModels.NodeAddressingElement]bool {
	addresses := map[*ciliumModels.NodeAddressingElement]bool{
		node.PrimaryAddress.IPV4: node.PrimaryAddress.IPV4.Enabled,
		node.PrimaryAddress.IPV6: node.PrimaryAddress.IPV6.Enabled,
	}
	for _, elem := range node.SecondaryAddresses {
		addresses[elem] = false
	}
	return addresses
}

// setNodes sets the list of nodes for the prober, and updates the pinger to
// start sending pings to all of the nodes.
func (p *prober) setNodes(nodes nodeMap) {
	p.Lock()
	defer p.Unlock()
	p.nodes = nodes

	for _, n := range nodes {
		for elem, primary := range getNodeAddresses(n) {
			if skipAddress(elem) {
				continue
			}

			network := "ip6:icmp"
			if isIPv4(elem.IP) {
				network = "ip4:icmp"
			}
			scopedLog := log.WithFields(logrus.Fields{
				logfields.NodeName: n.Name,
				logfields.IPAddr:   elem.IP,
				"primary":          primary,
			})

			result := &models.ConnectivityStatus{}
			ra, err := net.ResolveIPAddr(network, elem.IP)
			if err == nil {
				scopedLog.Debug("Probing for connectivity to node")
				result.Status = "Connection timed out"
				p.AddIPAddr(ra)
			} else {
				scopedLog.Debug("Skipping probe for node")
				result.Status = "Failed to resolve IP"
			}

			ip := ipString(elem.IP)
			if p.results[ip] == nil {
				p.results[ip] = &models.PathStatus{
					IP: elem.IP,
				}
			}
			p.results[ip].Icmp = result
		}
	}
}

// Run sends a single probes out to all of the other cilium nodes to gather
// connectivity status for the cluster.
func (p *prober) Run() error {
	// TODO: Launch goroutines to probe HTTP ports on other nodes

	err := p.Pinger.Run()
	// TODO: For each other probe, fetch error and return errors in
	//       priority of p.Pinger(), then connect(), etc.?
	return err
}

// RunLoop periodically sends probes out to all of the other cilium nodes to
// gather connectivity status for the cluster.
//
// This is a non-blocking method so it immediately returns. If you want to
// stop sending packets, call Done().
func (p *prober) RunLoop() {
	// FIXME: Spread the probes out across the probing interval
	p.Pinger.RunLoop()
}

// newPinger prepares a prober. The caller may invoke one the Run* methods of
// the prober to populate its 'results' map.
func newProber(s *Server, nodes nodeMap) *prober {
	prober := &prober{
		Pinger:  fastping.NewPinger(),
		server:  s,
		results: make(map[ipString]*models.PathStatus),
		nodes:   nodes,
	}
	prober.MaxRTT = s.ProbeDeadline

	prober.setNodes(nodes)
	prober.OnRecv = func(addr *net.IPAddr, rtt time.Duration) {
		prober.RLock()
		node := prober.nodes[ipString(addr.String())]
		prober.RUnlock()

		scopedLog := log.WithFields(logrus.Fields{
			logfields.IPAddr: addr,
			"rtt":            rtt,
		})
		if node == nil {
			scopedLog.Debugf("Node disappeared, skip result")
			return
		}

		prober.Lock()
		prober.results[ipString(addr.String())].Icmp = &models.ConnectivityStatus{
			Latency: rtt.Nanoseconds(),
			Status:  "",
		}
		prober.Unlock()

		scopedLog.WithFields(logrus.Fields{
			logfields.NodeName: node.Name,
		}).Debugf("Probe successful")
	}

	return prober
}
package option

import "github.com/sagernet/sing/common/json/badoption"

type SelectorOutboundOptions struct {
	Outbounds                 []string `json:"outbounds"`
	Default                   string   `json:"default,omitempty"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

type FallbackOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	Timeout                   badoption.Duration `json:"timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

type LoadBalanceStrategy string

const (
	LoadBalanceStrategyRoundRobin        LoadBalanceStrategy = "round-robin"
	LoadBalanceStrategyConsistentHashing LoadBalanceStrategy = "consistent-hashing"
	LoadBalanceStrategyStickySessions    LoadBalanceStrategy = "sticky-sessions"
)

type LoadBalanceOutboundOptions struct {
	Outbounds                 []string            `json:"outbounds"`
	URL                       string              `json:"url,omitempty"`
	Interval                  badoption.Duration  `json:"interval,omitempty"`
	IdleTimeout               badoption.Duration  `json:"idle_timeout,omitempty"`
	Timeout                   badoption.Duration  `json:"timeout,omitempty"`
	Strategy                  LoadBalanceStrategy `json:"strategy,omitempty"`
	InterruptExistConnections bool                `json:"interrupt_exist_connections,omitempty"`
}

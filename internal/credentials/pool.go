package credentials

import (
	"fmt"
	"sync"

	"github.com/98624017/meitu-layering-proxy/internal/config"
)

type Credential struct {
	Name     string
	AppKey   string
	SecretID string
	AppID    string
}

type Lease struct {
	ID         string
	Credential Credential
}

type Pool struct {
	mu      sync.Mutex
	entries []entry
	next    int
	seq     int64
}

type entry struct {
	credential     Credential
	maxConcurrency int
	activeLeases   map[string]struct{}
}

func NewPool(configs []config.CredentialConfig) *Pool {
	entries := make([]entry, 0, len(configs))
	for _, item := range configs {
		entries = append(entries, entry{
			credential: Credential{
				Name:     item.Name,
				AppKey:   item.AppKey,
				SecretID: item.SecretID,
				AppID:    item.AppID,
			},
			maxConcurrency: item.MaxConcurrency,
			activeLeases:   make(map[string]struct{}),
		})
	}
	return &Pool{entries: entries}
}

func (p *Pool) Lease(excluded map[string]struct{}) (Lease, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.entries) == 0 {
		return Lease{}, false
	}

	for offset := range p.entries {
		index := (p.next + offset) % len(p.entries)
		candidate := &p.entries[index]
		if _, ok := excluded[candidate.credential.Name]; ok {
			continue
		}
		if len(candidate.activeLeases) >= candidate.maxConcurrency {
			continue
		}

		p.seq++
		leaseID := fmt.Sprintf("%s-%d", candidate.credential.Name, p.seq)
		candidate.activeLeases[leaseID] = struct{}{}
		p.next = (index + 1) % len(p.entries)
		return Lease{
			ID:         leaseID,
			Credential: candidate.credential,
		}, true
	}

	return Lease{}, false
}

func (p *Pool) Release(leaseID string) bool {
	if leaseID == "" {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	for index := range p.entries {
		if _, ok := p.entries[index].activeLeases[leaseID]; ok {
			delete(p.entries[index].activeLeases, leaseID)
			return true
		}
	}
	return false
}

func (p *Pool) GetCredential(name string) (Credential, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, item := range p.entries {
		if item.credential.Name == name {
			return item.credential, true
		}
	}
	return Credential{}, false
}

func (p *Pool) ActiveCount(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, item := range p.entries {
		if item.credential.Name == name {
			return len(item.activeLeases)
		}
	}
	return 0
}

func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

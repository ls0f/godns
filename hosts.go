package main

import (
	"bufio"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	cluster2 "github.com/mediocregopher/radix.v2/cluster"
	"github.com/mediocregopher/radix.v2/redis"
	"golang.org/x/net/publicsuffix"
)

type Hosts struct {
	fileHosts       *FileHosts
	redisHosts      *RedisHosts
	refreshInterval time.Duration
}

func NewHosts(hs HostsSettings, rs RedisSettings) Hosts {
	fileHosts := &FileHosts{
		file:  hs.HostsFile,
		hosts: make(map[string]string),
	}

	var redisHosts *RedisHosts
	if hs.RedisEnable {
		rc, err := cluster2.NewWithOpts(cluster2.Opts{
			Addr: rs.Addr(),
			Dialer: func(network, addr string) (cli *redis.Client, err error) {
				conn, err := redis.DialTimeout(network, addr, 2*time.Second)
				if err != nil {
					return nil, err
				}
				if rs.Password != "" {
					if conn.Cmd("AUTH", rs.Password).Err != nil {
						logger.Error(err.Error())
						return nil, err
					}
				}
				return conn, nil

			},
		})
		if err != nil {
			logger.Error(err.Error())
		} else {

			redisHosts = &RedisHosts{
				redis: rc,
				key:   hs.RedisKey,
				hosts: make(map[string]string),
			}

			go redisHosts.KeepAlive()
		}
	}

	hosts := Hosts{fileHosts, redisHosts, time.Second * time.Duration(hs.RefreshInterval)}
	hosts.refresh()
	return hosts

}

/*
Match local /etc/hosts file first, remote redis records second
*/
func (h *Hosts) Get(domain string, family int) ([]net.IP, bool) {

	var sips []string
	var ip net.IP
	var ips []net.IP

	sips, ok := h.fileHosts.Get(domain)
	if !ok {
		if h.redisHosts != nil {
			sips, ok = h.redisHosts.Get(domain)
		}
	}

	if sips == nil {
		return nil, false
	}

	for _, sip := range sips {
		switch family {
		case _IP4Query:
			ip = net.ParseIP(sip).To4()
		case _IP6Query:
			ip = net.ParseIP(sip).To16()
		default:
			continue
		}
		if ip != nil {
			ips = append(ips, ip)
		}
	}

	return ips, (ips != nil)
}

/*
Update hosts records from /etc/hosts file and redis per minute
*/
func (h *Hosts) refresh() {
	ticker := time.NewTicker(h.refreshInterval)
	go func() {
		for {
			h.fileHosts.Refresh()
			if h.redisHosts != nil {
				h.redisHosts.Refresh()
			}
			<-ticker.C
		}
	}()
}

type RedisHosts struct {
	redis        *cluster2.Cluster
	key          string
	hosts        map[string]string
	mu           sync.RWMutex
	pingInterval time.Duration
}

func (r *RedisHosts) KeepAlive() {
	if r.pingInterval == 0 {
		r.pingInterval = time.Second * 60
	}
	for {
		<-time.After(r.pingInterval)
		// redis cluster heart beat.
		clients, err := r.redis.GetEvery()
		if err != nil {
			logger.Error(err.Error())
			// unable to do redis heart beat, log the error.
			continue
		}
		for _, c := range clients {
			if err := c.Cmd("PING").Err; err != nil {
				logger.Error("redis ping: %s", err)
				// unable to keep redis conn alive, log the error.
				c.Close()
				continue
			}
			r.redis.Put(c)
		}
		// finished redis heart beat.
	}
}

func (r *RedisHosts) Get(domain string) ([]string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	domain = strings.ToLower(domain)
	ip, ok := r.hosts[domain]
	if ok {
		return strings.Split(ip, ","), true
	}

	sld, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return nil, false
	}

	for host, ip := range r.hosts {
		if strings.HasPrefix(host, "*.") {
			old, err := publicsuffix.EffectiveTLDPlusOne(host)
			if err != nil {
				continue
			}
			if sld == old {
				return strings.Split(ip, ","), true
			}
		}
	}
	return nil, false
}

func (r *RedisHosts) Set(domain, ip string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return true, r.redis.Cmd("HSET", r.key, strings.ToLower(domain), []byte(ip)).Err
}

func (r *RedisHosts) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clear()
	rsp := r.redis.Cmd("HGETALL", r.key)
	var err error
	r.hosts, err = rsp.Map()
	if err != nil {
		logger.Warn("Update hosts records from redis failed %s", err)
	} else {
		logger.Debug("Update hosts records from redis")
	}
}

func (r *RedisHosts) clear() {
	r.hosts = make(map[string]string)
}

type FileHosts struct {
	file  string
	hosts map[string]string
	mu    sync.RWMutex
}

func (f *FileHosts) Get(domain string) ([]string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	domain = strings.ToLower(domain)
	ip, ok := f.hosts[domain]
	if ok {
		return []string{ip}, true
	}

	sld, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return nil, false
	}

	for host, ip := range f.hosts {
		if strings.HasPrefix(host, "*.") {
			old, err := publicsuffix.EffectiveTLDPlusOne(host)
			if err != nil {
				continue
			}
			if sld == old {
				return []string{ip}, true
			}
		}
	}

	return nil, false
}

func (f *FileHosts) Refresh() {
	buf, err := os.Open(f.file)
	if err != nil {
		logger.Warn("Update hosts records from file failed %s", err)
		return
	}
	defer buf.Close()

	f.mu.Lock()
	defer f.mu.Unlock()

	f.clear()

	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {

		line := scanner.Text()
		line = strings.TrimSpace(line)
		line = strings.Replace(line, "\t", " ", -1)

		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		sli := strings.Split(line, " ")

		if len(sli) < 2 {
			continue
		}

		ip := sli[0]
		if !f.isIP(ip) {
			continue
		}

		// Would have multiple columns of domain in line.
		// Such as "127.0.0.1  localhost localhost.domain" on linux.
		// The domains may not strict standard, like "local" so don't check with f.isDomain(domain).
		for i := 1; i <= len(sli)-1; i++ {
			domain := strings.TrimSpace(sli[i])
			if domain == "" {
				continue
			}

			f.hosts[strings.ToLower(domain)] = ip
		}
	}
	logger.Debug("update hosts records from %s, total %d records.", f.file, len(f.hosts))
}

func (f *FileHosts) clear() {
	f.hosts = make(map[string]string)
}

func (f *FileHosts) isDomain(domain string) bool {
	if f.isIP(domain) {
		return false
	}
	match, _ := regexp.MatchString(`^([a-zA-Z0-9\*]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,6}$`, domain)
	return match
}

func (f *FileHosts) isIP(ip string) bool {
	return (net.ParseIP(ip) != nil)
}

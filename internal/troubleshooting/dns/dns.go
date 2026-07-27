// Package dns performs a breadth-first subdomain-enumeration DNS crawl,
// starting from a hardcoded list of common subdomain prefixes and following
// CNAME/MX/SRV targets that stay within the root domain. Ported from a
// hand-rolled raw-UDP Java implementation; here message building/parsing is
// delegated to github.com/miekg/dns instead of re-deriving RFC 1035 wire
// format by hand.
package dns

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"foilen-box/internal/troubleshooting/model"
)

const (
	defaultNameServer = "8.8.8.8:53"
	queryTimeout      = 5 * time.Second
)

var queryTypes = []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeCNAME, dns.TypeMX, dns.TypeNS, dns.TypeTXT, dns.TypeSRV}

// commonSubdomainPrefixes seeds the crawl, same list as the Java version.
var commonSubdomainPrefixes = []string{
	"ns1", "ns2", "ns3", "ns4",
	"beta", "dev", "pre", "www", "w3",
	"_dmarc", "_imap._tcp", "_imaps._tcp", "_submission._tcp", "_pop._tcp", "_pop3._tcp",
	"autodiscover", "email", "imap", "mail", "mx", "pop", "pop3", "smtp",
	"default._domainkey", "_amazonses", "k2._domainkey", "k3._domainkey",
	"mandrill._domainkey", "s1._domainkey", "s2._domainkey",
	"selector1._domainkey", "selector2._domainkey",
	"_caldavs._tcp",
	"cpanel", "whm",
	"_sip._tls", "_sipfederationtls._tcp", "sip",
	"_jabber._tcp", "_ldap._tcp", "_xmpp-client._tcp",
	"enterpriseenrollment", "enterpriseregistration", "ftp", "login",
	"lyncdiscover", "lyncdiscoverinternal", "gitlab", "phpmyadmin",
	"sso", "shop", "unifi", "unms", "webdisk", "webmail",
	"_acme-challenge",
	"asuid",
}

// Query performs the DNS crawl against the default nameserver (8.8.8.8).
func Query(domainName string) []model.RawDnsEntry {
	return QueryUsingServer(domainName, "")
}

// QueryUsingServer performs the DNS crawl against usingDNSServer, or the
// default nameserver if empty.
func QueryUsingServer(domainName, usingDNSServer string) []model.RawDnsEntry {
	nameServer := defaultNameServer
	if usingDNSServer != "" {
		nameServer = ensurePort(usingDNSServer)
	}

	var queue []string
	queue = append(queue, domainName)
	for _, sub := range commonSubdomainPrefixes {
		queue = append(queue, sub+"."+domainName)
	}

	var (
		mu         sync.Mutex
		visited    = map[string]bool{}
		results    []model.RawDnsEntry
		discovered []string
	)

	for len(queue) > 0 {
		var wg sync.WaitGroup
		toVisit := queue
		queue = nil

		for _, hostname := range toVisit {
			mu.Lock()
			already := visited[hostname]
			visited[hostname] = true
			mu.Unlock()
			if already {
				continue
			}

			for _, qtype := range queryTypes {
				wg.Add(1)
				go func(hostname string, qtype uint16) {
					defer wg.Done()
					entries, newHosts := queryOne(hostname, qtype, nameServer, domainName)
					mu.Lock()
					results = append(results, entries...)
					discovered = append(discovered, newHosts...)
					mu.Unlock()
				}(hostname, qtype)
			}
		}

		wg.Wait()

		queue = append(queue, discovered...)
		discovered = nil
	}

	return dedupSorted(results)
}

func ensurePort(server string) string {
	if strings.Contains(server, ":") {
		return server
	}
	return server + ":53"
}

func queryOne(hostname string, qtype uint16, nameServer, rootDomain string) (entries []model.RawDnsEntry, discovered []string) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(hostname), qtype)
	msg.RecursionDesired = true

	client := &dns.Client{Timeout: queryTimeout}
	resp, _, err := client.Exchange(msg, nameServer)
	if err != nil || resp == nil {
		return nil, nil
	}

	for _, rr := range resp.Answer {
		header := rr.Header()
		entry := model.RawDnsEntry{Name: hostname, TTL: header.Ttl}

		switch v := rr.(type) {
		case *dns.A:
			entry.Type = "A"
			entry.Details = v.A.String()
			entries = append(entries, entry)
		case *dns.AAAA:
			entry.Type = "AAAA"
			entry.Details = v.AAAA.String()
			entries = append(entries, entry)
		case *dns.CNAME:
			entry.Type = "CNAME"
			target := trimDot(v.Target)
			entry.Details = target
			entries = append(entries, entry)
			if strings.HasSuffix(target, rootDomain) {
				discovered = append(discovered, target)
			}
		case *dns.NS:
			entry.Type = "NS"
			entry.Details = trimDot(v.Ns)
			entries = append(entries, entry)
		case *dns.MX:
			entry.Type = "MX"
			target := trimDot(v.Mx)
			prio := int(v.Preference)
			entry.Priority = &prio
			entry.Details = target
			entries = append(entries, entry)
			if strings.HasSuffix(target, rootDomain) {
				discovered = append(discovered, target)
			}
		case *dns.TXT:
			for _, txt := range v.Txt {
				entries = append(entries, model.RawDnsEntry{
					Name: hostname, Type: "TXT", TTL: header.Ttl, Details: txt,
				})
			}
		case *dns.SRV:
			target := trimDot(v.Target)
			prio := int(v.Priority)
			weight := int(v.Weight)
			port := int(v.Port)
			entry.Type = "SRV"
			entry.Priority = &prio
			entry.Weight = &weight
			entry.Port = &port
			entry.Details = target
			entries = append(entries, entry)
			if strings.HasSuffix(target, rootDomain) {
				discovered = append(discovered, target)
			}
		}
	}

	return entries, discovered
}

func trimDot(name string) string {
	return strings.TrimSuffix(name, ".")
}

func dedupSorted(entries []model.RawDnsEntry) []model.RawDnsEntry {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Less(entries[j]) })

	result := make([]model.RawDnsEntry, 0, len(entries))
	for i, e := range entries {
		if i > 0 && e.Equal(entries[i-1]) {
			continue
		}
		result = append(result, e)
	}
	return result
}

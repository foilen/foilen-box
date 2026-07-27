// Package whois resolves domain registration data via a plain TCP
// connection to the appropriate WHOIS server (port 43), matching the
// original hand-rolled Java implementation (no third-party WHOIS library).
package whois

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"foilen-box/internal/troubleshooting/model"
)

const timeout = 10 * time.Second

// whoisServers is intentionally limited to the same 5 TLDs the Java version
// supported.
var whoisServers = map[string]model.WhoisServerConfig{
	".ca":  {ServerName: "whois.cira.ca", QueryPrefix: ""},
	".com": {ServerName: "whois.internic.net", QueryPrefix: "="},
	".edu": {ServerName: "whois.internic.net", QueryPrefix: "="},
	".org": {ServerName: "whois.pir.org", QueryPrefix: ""},
	".net": {ServerName: "whois.internic.net", QueryPrefix: "="},
}

// Query looks up WHOIS data for domainName. Returns an error if the TLD
// isn't one of the 5 configured, or if the network query fails.
func Query(domainName string) (*model.WhoisResponse, error) {
	lastDot := strings.LastIndex(domainName, ".")
	if lastDot < 0 {
		return nil, fmt.Errorf("cannot determine TLD for %s", domainName)
	}
	tld := domainName[lastDot:]
	cfg, ok := whoisServers[tld]
	if !ok {
		return nil, fmt.Errorf("no WHOIS server configured for TLD %s", tld)
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.ServerName, "43"), timeout)
	if err != nil {
		return nil, fmt.Errorf("whois query failed for %s: %w", domainName, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	if _, err := fmt.Fprintf(conn, "%s%s\n", cfg.QueryPrefix, domainName); err != nil {
		return nil, fmt.Errorf("whois query failed for %s: %w", domainName, err)
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
		sb.WriteString("\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("whois query failed for %s: %w", domainName, err)
	}

	return Parse(sb.String()), nil
}

const whoisFieldDateLayout = "2006-01-02T15:04:05Z"

// Parse parses a raw WHOIS text response into a WhoisResponse, mirroring the
// Java whoisParser: split each line on the first ':', match ~50 known keys,
// and bucket everything else into Others as "key: value".
func Parse(whoisData string) *model.WhoisResponse {
	resp := &model.WhoisResponse{}
	for _, line := range strings.Split(whoisData, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "Domain Name":
			resp.DomainName = value
		case "Creation Date":
			resp.CreationDate = parseDate(value)
		case "Updated Date":
			resp.UpdatedDate = parseDate(value)
		case "Registry Expiry Date":
			resp.ExpiryDate = parseDate(value)
		case "Registrar":
			resp.RegistrarName = value
		case "Registrar WHOIS Server":
			resp.RegistrarWhoisServer = value
		case "Registrar URL":
			resp.RegistrarURL = value
		case "Registrant Name":
			resp.RegistrantName = value
		case "Registrant Organization":
			resp.RegistrantOrganization = value
		case "Registrant Street":
			resp.RegistrantStreet = value
		case "Registrant City":
			resp.RegistrantCity = value
		case "Registrant State/Province":
			resp.RegistrantStateProv = value
		case "Registrant Postal Code":
			resp.RegistrantPostalCode = value
		case "Registrant Country":
			resp.RegistrantCountry = value
		case "Registrant Phone":
			resp.RegistrantPhone = value
		case "Registrant Phone Ext":
			resp.RegistrantPhoneExt = value
		case "Registrant Fax":
			resp.RegistrantFax = value
		case "Registrant Fax Ext":
			resp.RegistrantFaxExt = value
		case "Registrant Email":
			resp.RegistrantEmail = value
		case "Admin Name":
			resp.AdminName = value
		case "Admin Organization":
			resp.AdminOrganization = value
		case "Admin Street":
			resp.AdminStreet = value
		case "Admin City":
			resp.AdminCity = value
		case "Admin State/Province":
			resp.AdminStateProv = value
		case "Admin Postal Code":
			resp.AdminPostalCode = value
		case "Admin Country":
			resp.AdminCountry = value
		case "Admin Phone":
			resp.AdminPhone = value
		case "Admin Phone Ext":
			resp.AdminPhoneExt = value
		case "Admin Fax":
			resp.AdminFax = value
		case "Admin Fax Ext":
			resp.AdminFaxExt = value
		case "Admin Email":
			resp.AdminEmail = value
		case "Tech Name":
			resp.TechName = value
		case "Tech Organization":
			resp.TechOrganization = value
		case "Tech Street":
			resp.TechStreet = value
		case "Tech City":
			resp.TechCity = value
		case "Tech State/Province":
			resp.TechStateProv = value
		case "Tech Postal Code":
			resp.TechPostalCode = value
		case "Tech Country":
			resp.TechCountry = value
		case "Tech Phone":
			resp.TechPhone = value
		case "Tech Phone Ext":
			resp.TechPhoneExt = value
		case "Tech Fax":
			resp.TechFax = value
		case "Tech Fax Ext":
			resp.TechFaxExt = value
		case "Tech Email":
			resp.TechEmail = value
		case "Billing Name":
			resp.BillingName = value
		case "Billing Organization":
			resp.BillingOrganization = value
		case "Billing Street":
			resp.BillingStreet = value
		case "Billing City":
			resp.BillingCity = value
		case "Billing State/Province":
			resp.BillingStateProv = value
		case "Billing Postal Code":
			resp.BillingPostalCode = value
		case "Billing Country":
			resp.BillingCountry = value
		case "Billing Phone":
			resp.BillingPhone = value
		case "Billing Phone Ext":
			resp.BillingPhoneExt = value
		case "Billing Fax":
			resp.BillingFax = value
		case "Billing Fax Ext":
			resp.BillingFaxExt = value
		case "Billing Email":
			resp.BillingEmail = value
		case "DNSSEC":
			resp.DNSSec = value
		case "Name Server":
			resp.NameServers = append(resp.NameServers, value)
		default:
			resp.Others = append(resp.Others, key+": "+value)
		}
	}
	return resp
}

func parseDate(value string) *time.Time {
	t, err := time.Parse(whoisFieldDateLayout, value)
	if err != nil {
		return nil
	}
	return &t
}

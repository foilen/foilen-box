package whois

import (
	"testing"
	"time"
)

const sampleResponse = `Domain Name: EXAMPLE.COM
Registry Domain ID: 123456_DOMAIN_COM-VRSN
Registrar WHOIS Server: whois.example-registrar.com
Registrar URL: http://www.example-registrar.com
Updated Date: 2024-08-14T04:39:03Z
Creation Date: 1995-08-14T04:00:00Z
Registry Expiry Date: 2025-08-13T04:00:00Z
Registrar: Example Registrar, LLC
Registrant Name: Domain Administrator
Registrant Organization: Example Corp
Registrant Street: 123 Example Way
Registrant City: Springfield
Registrant State/Province: CA
Registrant Postal Code: 94000
Registrant Country: US
Registrant Email: admin@example.com
Name Server: A.IANA-SERVERS.NET
Name Server: B.IANA-SERVERS.NET
DNSSEC: unsigned
>>> Last update of WHOIS database: 2025-01-01T00:00:00Z <<<
`

func TestParse(t *testing.T) {
	resp := Parse(sampleResponse)

	if resp.DomainName != "EXAMPLE.COM" {
		t.Errorf("DomainName = %q, want EXAMPLE.COM", resp.DomainName)
	}
	if resp.RegistrarName != "Example Registrar, LLC" {
		t.Errorf("RegistrarName = %q", resp.RegistrarName)
	}
	if resp.RegistrarWhoisServer != "whois.example-registrar.com" {
		t.Errorf("RegistrarWhoisServer = %q", resp.RegistrarWhoisServer)
	}
	if resp.RegistrantOrganization != "Example Corp" {
		t.Errorf("RegistrantOrganization = %q", resp.RegistrantOrganization)
	}
	if resp.DNSSec != "unsigned" {
		t.Errorf("DNSSec = %q", resp.DNSSec)
	}
	if len(resp.NameServers) != 2 || resp.NameServers[0] != "A.IANA-SERVERS.NET" {
		t.Errorf("NameServers = %v", resp.NameServers)
	}

	wantCreation := time.Date(1995, 8, 14, 4, 0, 0, 0, time.UTC)
	if resp.CreationDate == nil || !resp.CreationDate.Equal(wantCreation) {
		t.Errorf("CreationDate = %v, want %v", resp.CreationDate, wantCreation)
	}

	wantExpiry := time.Date(2025, 8, 13, 4, 0, 0, 0, time.UTC)
	if resp.ExpiryDate == nil || !resp.ExpiryDate.Equal(wantExpiry) {
		t.Errorf("ExpiryDate = %v, want %v", resp.ExpiryDate, wantExpiry)
	}

	// Unrecognized keys (Registry Domain ID, the ">>> Last update..." line
	// has no colon-splittable "key: value" of a known field) land in Others.
	foundRegistryDomainID := false
	for _, other := range resp.Others {
		if other == "Registry Domain ID: 123456_DOMAIN_COM-VRSN" {
			foundRegistryDomainID = true
		}
	}
	if !foundRegistryDomainID {
		t.Errorf("expected Registry Domain ID in Others, got %v", resp.Others)
	}
}

func TestParseUnparseableDate(t *testing.T) {
	resp := Parse("Creation Date: not-a-date\n")
	if resp.CreationDate != nil {
		t.Errorf("CreationDate = %v, want nil for unparseable date", resp.CreationDate)
	}
}

func TestParseEmpty(t *testing.T) {
	resp := Parse("")
	if resp.DomainName != "" || len(resp.Others) != 0 {
		t.Errorf("expected empty response, got %+v", resp)
	}
}

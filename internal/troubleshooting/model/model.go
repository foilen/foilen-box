// Package model holds the data types produced by DNS and WHOIS lookups.
package model

import "time"

// RawDnsEntry is a single resource record discovered during the DNS crawl.
// Priority/Weight/Port are nil when not applicable to the record type
// (matching Java's nullable Integer fields).
type RawDnsEntry struct {
	Name     string
	Type     string
	Details  string
	Priority *int
	Weight   *int
	Port     *int
	TTL      uint32
}

// Less orders entries the same way Java's RawDnsEntry.compareTo did: by
// Name, Type, Details, then Priority/Weight/Port (nil treated as 0).
func (e RawDnsEntry) Less(o RawDnsEntry) bool {
	if e.Name != o.Name {
		return e.Name < o.Name
	}
	if e.Type != o.Type {
		return e.Type < o.Type
	}
	if e.Details != o.Details {
		return e.Details < o.Details
	}
	if intOrZero(e.Priority) != intOrZero(o.Priority) {
		return intOrZero(e.Priority) < intOrZero(o.Priority)
	}
	if intOrZero(e.Weight) != intOrZero(o.Weight) {
		return intOrZero(e.Weight) < intOrZero(o.Weight)
	}
	return intOrZero(e.Port) < intOrZero(o.Port)
}

// Equal reports whether two entries have the same field values, used for
// de-duplication (matches Java's RawDnsEntry.equals).
func (e RawDnsEntry) Equal(o RawDnsEntry) bool {
	return e.Name == o.Name && e.Type == o.Type && e.Details == o.Details &&
		intOrZero(e.Priority) == intOrZero(o.Priority) &&
		intOrZero(e.Weight) == intOrZero(o.Weight) &&
		intOrZero(e.Port) == intOrZero(o.Port)
}

func intOrZero(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func IntPtr(v int) *int {
	return &v
}

// WhoisServerConfig identifies the WHOIS server and query-line prefix to use
// for a given TLD.
type WhoisServerConfig struct {
	ServerName  string
	QueryPrefix string
}

// WhoisResponse holds the parsed fields of a WHOIS text response. Contact
// blocks (Registrant/Admin/Tech/Billing) mirror the WHOIS spec's field set.
type WhoisResponse struct {
	DomainName           string
	RegistrarName        string
	RegistrarWhoisServer string
	RegistrarURL         string
	CreationDate         *time.Time
	UpdatedDate          *time.Time
	ExpiryDate           *time.Time

	RegistrantName         string
	RegistrantOrganization string
	RegistrantStreet       string
	RegistrantCity         string
	RegistrantStateProv    string
	RegistrantPostalCode   string
	RegistrantCountry      string
	RegistrantPhone        string
	RegistrantPhoneExt     string
	RegistrantFax          string
	RegistrantFaxExt       string
	RegistrantEmail        string

	AdminName         string
	AdminOrganization string
	AdminStreet       string
	AdminCity         string
	AdminStateProv    string
	AdminPostalCode   string
	AdminCountry      string
	AdminPhone        string
	AdminPhoneExt     string
	AdminFax          string
	AdminFaxExt       string
	AdminEmail        string

	TechName         string
	TechOrganization string
	TechStreet       string
	TechCity         string
	TechStateProv    string
	TechPostalCode   string
	TechCountry      string
	TechPhone        string
	TechPhoneExt     string
	TechFax          string
	TechFaxExt       string
	TechEmail        string

	BillingName         string
	BillingOrganization string
	BillingStreet       string
	BillingCity         string
	BillingStateProv    string
	BillingPostalCode   string
	BillingCountry      string
	BillingPhone        string
	BillingPhoneExt     string
	BillingFax          string
	BillingFaxExt       string
	BillingEmail        string

	DNSSec      string
	NameServers []string
	Others      []string
}

const whoisDisplayLayout = "2006-01-02 15:04:05 MST"

func formatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(whoisDisplayLayout)
}

func (w *WhoisResponse) CreationDateText() string { return formatDate(w.CreationDate) }
func (w *WhoisResponse) UpdatedDateText() string  { return formatDate(w.UpdatedDate) }
func (w *WhoisResponse) ExpiryDateText() string   { return formatDate(w.ExpiryDate) }

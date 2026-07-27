// Package report renders WHOIS/DNS lookup results as the same plain-text
// report the desktop UI used to build directly in internal/ui/troubleshooting.go.
package report

import (
	"fmt"
	"strconv"
	"strings"

	tmodel "foilen-box/internal/troubleshooting/model"
)

// Format combines the WHOIS response (nil if the lookup failed) and DNS
// entries into the full report text.
func Format(whoisResp *tmodel.WhoisResponse, dnsEntries []tmodel.RawDnsEntry) string {
	var sb strings.Builder
	if whoisResp == nil {
		sb.WriteString("=== WHOIS: Failed to retrieve ===\n")
	} else {
		sb.WriteString(formatWhois(whoisResp))
	}
	sb.WriteString("\n")
	sb.WriteString(formatDNS(dnsEntries))
	return sb.String()
}

func formatWhois(w *tmodel.WhoisResponse) string {
	var sb strings.Builder
	sb.WriteString("=== WHOIS Information ===\n")
	appendIfSet(&sb, "Domain Name", w.DomainName)
	appendIfSet(&sb, "Registrar", w.RegistrarName)
	appendIfSet(&sb, "Registrar WHOIS Server", w.RegistrarWhoisServer)
	appendIfSet(&sb, "Registrar URL", w.RegistrarURL)
	appendIfSet(&sb, "Creation Date", w.CreationDateText())
	appendIfSet(&sb, "Updated Date", w.UpdatedDateText())
	appendIfSet(&sb, "Expiry Date", w.ExpiryDateText())
	appendIfSet(&sb, "DNSSEC", w.DNSSec)

	appendContactBlock(&sb, "Registrant", w.RegistrantName, w.RegistrantOrganization, w.RegistrantStreet,
		w.RegistrantCity, w.RegistrantStateProv, w.RegistrantPostalCode, w.RegistrantCountry,
		w.RegistrantPhone, w.RegistrantPhoneExt, w.RegistrantFax, w.RegistrantFaxExt, w.RegistrantEmail)

	appendContactBlock(&sb, "Admin", w.AdminName, w.AdminOrganization, w.AdminStreet,
		w.AdminCity, w.AdminStateProv, w.AdminPostalCode, w.AdminCountry,
		w.AdminPhone, w.AdminPhoneExt, w.AdminFax, w.AdminFaxExt, w.AdminEmail)

	appendContactBlock(&sb, "Tech", w.TechName, w.TechOrganization, w.TechStreet,
		w.TechCity, w.TechStateProv, w.TechPostalCode, w.TechCountry,
		w.TechPhone, w.TechPhoneExt, w.TechFax, w.TechFaxExt, w.TechEmail)

	appendContactBlock(&sb, "Billing", w.BillingName, w.BillingOrganization, w.BillingStreet,
		w.BillingCity, w.BillingStateProv, w.BillingPostalCode, w.BillingCountry,
		w.BillingPhone, w.BillingPhoneExt, w.BillingFax, w.BillingFaxExt, w.BillingEmail)

	if len(w.NameServers) > 0 {
		sb.WriteString("\n--- Name Servers ---\n")
		for _, ns := range w.NameServers {
			sb.WriteString("  " + ns + "\n")
		}
	}

	if len(w.Others) > 0 {
		sb.WriteString("\n--- Other ---\n")
		for _, other := range w.Others {
			sb.WriteString("  " + other + "\n")
		}
	}
	return sb.String()
}

func appendContactBlock(sb *strings.Builder, label string, name, org, street, city, state, postal, country, phone, phoneExt, fax, faxExt, email string) {
	has := name != "" || org != "" || street != "" || city != "" || state != "" || postal != "" ||
		country != "" || phone != "" || phoneExt != "" || fax != "" || faxExt != "" || email != ""
	if !has {
		return
	}
	sb.WriteString("\n--- " + label + " ---\n")
	appendIfSet(sb, "Name", name)
	appendIfSet(sb, "Organization", org)
	appendIfSet(sb, "Street", street)
	appendIfSet(sb, "City", city)
	appendIfSet(sb, "State/Province", state)
	appendIfSet(sb, "Postal Code", postal)
	appendIfSet(sb, "Country", country)
	appendIfSet(sb, "Phone", phone)
	appendIfSet(sb, "Phone Ext", phoneExt)
	appendIfSet(sb, "Fax", fax)
	appendIfSet(sb, "Fax Ext", faxExt)
	appendIfSet(sb, "Email", email)
}

func appendIfSet(sb *strings.Builder, key, value string) {
	if value != "" {
		fmt.Fprintf(sb, "  %-25s %s\n", key+":", value)
	}
}

func formatDNS(entries []tmodel.RawDnsEntry) string {
	if len(entries) == 0 {
		return "=== DNS Records: none found ===\n"
	}
	var sb strings.Builder
	sb.WriteString("=== DNS Records ===\n")
	fmt.Fprintf(&sb, "  %-50s %-8s %-6s %-6s %-6s %-8s %s\n", "Name", "Type", "Prio", "Weight", "Port", "TTL", "Details")
	sb.WriteString("  " + strings.Repeat("-", 120) + "\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "  %-50s %-8s %-6s %-6s %-6s %-8s %s\n",
			e.Name, e.Type, intPtrString(e.Priority), intPtrString(e.Weight), intPtrString(e.Port),
			strconv.FormatUint(uint64(e.TTL), 10), e.Details)
	}
	return sb.String()
}

func intPtrString(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

package tour

import "strings"

// Doc points to where the official documentation describes a step: a page
// of the API reference, and for Nexi profiles a page of the Nexi guide.
type Doc struct {
	API, APITitle     string // path and title in the API reference
	Guide, GuideTitle string // a page of the Nexi documentation (Nexi profiles only)
}

// The API references of both platforms share their paths; the guides are
// Nexi's.
var refHosts = map[string]string{
	"nexi":     "https://paygate-nexigroup.redocly.app",
	"computop": "https://computop-docs.redocly.app",
}

const nexiGuide = "https://docs.paygate.nexigroup.com/nexi-developer/"

func isComputop(brand string) bool { return strings.EqualFold(brand, "computop") }

// URL is the page for a profile of brand ("nexi" or "computop").
func (d Doc) URL(brand string) string {
	if d.Guide != "" && !isComputop(brand) {
		return d.Guide
	}
	if d.API == "" {
		return ""
	}
	host := refHosts["nexi"]
	if isComputop(brand) {
		host = refHosts["computop"]
	}
	return host + d.API
}

// Label names the page the URL opens.
func (d Doc) Label(brand string) string {
	if d.Guide != "" && !isComputop(brand) {
		return T{"Nexi guide: ", "Nexi-Anleitung: "}.String() + d.GuideTitle
	}
	if d.API == "" {
		return ""
	}
	return T{"API reference: ", "API-Referenz: "}.String() + d.APITitle
}

var (
	docSession = Doc{API: "/payments/createsession", APITitle: "Create checkout session"}
	docHosted  = Doc{API: "/payments/createsession", APITitle: "Create checkout session",
		Guide: nexiGuide + "rest-api-hosted-payment-page", GuideTitle: "REST API Hosted Payment Page"}
	docDirect = Doc{API: "/payments/createpayment", APITitle: "Create payment",
		Guide: nexiGuide + "rest-api-direct-integration", GuideTitle: "REST API Direct Integration"}
	docCreate   = Doc{API: "/payments/createpayment", APITitle: "Create payment"}
	docStatus   = Doc{API: "/payments/getpaymentdetailsbytransactionid", APITitle: "Retrieve payment details by Transaction ID"}
	docCapture  = Doc{API: "/payments/capturepayment", APITitle: "Capture payment"}
	docRefund   = Doc{API: "/payments/refundpayment", APITitle: "Refund payment with reference"}
	docReverse  = Doc{API: "/payments/reversepayment", APITitle: "Reverse payment"}
	docWebhooks = Doc{API: "/webhooks", APITitle: "Webhooks",
		Guide: nexiGuide + "rest-api-webhooks", GuideTitle: "REST API Webhooks"}
	docSignature = Doc{API: "/webhooks", APITitle: "Webhooks",
		Guide: nexiGuide + "rest-api-webhooks#Security-&-verification", GuideTitle: "REST API Webhooks · Security & verification"}
	docStatusByPayID = Doc{API: "/payments/getpaymentdetailsbypaymentid", APITitle: "Retrieve payment details by Payment ID"}
	docLink          = Doc{API: "/payments/createpaymentlink", APITitle: "Create payment link"}
	docForms         = Doc{API: "/payments/createpayment", APITitle: "Create payment",
		Guide: nexiGuide + "rest-api-hosted-forms", GuideTitle: "REST API Hosted Forms"}
	docBinCheck = Doc{API: "/services/binlookup", APITitle: "Get card country and DCC eligibility information",
		Guide: nexiGuide + "rest-api-edcc", GuideTitle: "REST API eDCC"}
	docQuote = Doc{API: "/services/dccquote", APITitle: "Get Dynamic Currency Conversion quote",
		Guide: nexiGuide + "rest-api-edcc", GuideTitle: "REST API eDCC"}
	docEDCC = Doc{API: "/payments/createpayment", APITitle: "Create payment",
		Guide: nexiGuide + "rest-api-edcc", GuideTitle: "REST API eDCC"}
)

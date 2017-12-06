package matchers

import (
	"time"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"code.cloudfoundry.org/route-emitter/routingtable"
	"github.com/onsi/gomega/types"
)

func MatchRegistryMessage(message routingtable.RegistryMessage) types.GomegaMatcher {
	uris := []interface{}{}
	for _, uri := range message.URIs {
		uris = append(uris, uri)
	}

	return MatchAllFields(Fields{
		"Host":                 Equal(message.Host),
		"Port":                 Equal(message.Port),
		"TlsPort":              Equal(message.TlsPort),
		"URIs":                 ConsistOf(uris...),
		"App":                  Equal(message.App),
		"RouteServiceUrl":      Equal(message.RouteServiceUrl),
		"PrivateInstanceId":    Equal(message.PrivateInstanceId),
		"PrivateInstanceIndex": Equal(message.PrivateInstanceIndex),
		"ServerCertDomainSAN":  Equal(message.ServerCertDomainSAN),
		"IsolationSegment":     Equal(message.IsolationSegment),
		"EndpointUpdatedAtNs":  BeNumerically("~", time.Now().UnixNano(), time.Minute),
		"Tags":                 Equal(message.Tags),
	})
}

package routing_table

type RegistryMessage struct {
	Host              string   `json:"host"`
	Port              uint32   `json:"port"`
	URIs              []string `json:"uris"`
	App               string   `json:"app,omitempty"`
	RouteServiceUrl   string   `json:"route_service_url,omitempty"`
	PrivateInstanceId string   `json:"private_instance_id,omitempty"`
}

func RegistryMessageFor(endpoint Endpoint, routes Routes) RegistryMessage {
	return RegistryMessage{
		URIs: routes.Hostnames,
		Host: endpoint.Host,
		Port: endpoint.Port,
		App:  routes.LogGuid,

		PrivateInstanceId: endpoint.InstanceGuid,
		RouteServiceUrl:   routes.RouteServiceUrl,
	}
}

type RouterGreetingMessage struct {
	MinimumRegisterInterval int `json:"minimumRegisterIntervalInSeconds"`
	PruneThresholdInSeconds int `json:"pruneThresholdInSeconds"`
}

package routing_table

type RegistryMessage struct {
	Host string   `json:"host"`
	Port uint16   `json:"port"`
	URIs []string `json:"uris"`
	App  string   `json:"app,omitempty"`

	PrivateInstanceId string `json:"private_instance_id,omitempty"`
}

func RegistryMessageFor(endpoint Endpoint, routes Routes) RegistryMessage {
	return RegistryMessage{
		URIs: routes.Hostnames,
		Host: endpoint.Host,
		Port: endpoint.Port,

		App: routes.LogGuid,

		PrivateInstanceId: endpoint.InstanceGuid,
	}
}

type RouterGreetingMessage struct {
	MinimumRegisterInterval int `json:"minimumRegisterIntervalInSeconds"`
	PruneThresholdInSeconds int `json:"pruneThresholdInSeconds"`
}

package routing_table

type RegistryMessage struct {
	Host string   `json:"host"`
	Port uint16   `json:"port"`
	URIs []string `json:"uris"`
	App  string   `json:"app,omitempty"`

	PrivateInstanceId string `json:"private_instance_id,omitempty"`
}

func RegistryMessageFor(container Container, routes Routes) RegistryMessage {
	return RegistryMessage{
		URIs: routes.URIs,
		Host: container.Host,
		Port: container.Port,

		App: routes.LogGuid,

		PrivateInstanceId: container.InstanceGuid,
	}
}

type RouterGreetingMessage struct {
	MinimumRegisterInterval int `json:"minimumRegisterIntervalInSeconds"`
}

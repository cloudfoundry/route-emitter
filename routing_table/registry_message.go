package routing_table

type RegistryMessage struct {
	Host string   `json:"host"`
	Port uint16   `json:"port"`
	URIs []string `json:"uris"`
	App  string   `json:"app,omitempty"`

	PrivateInstanceId string `json:"private_instance_id,omitempty"`
}

type RouterGreetingMessage struct {
	MinimumRegisterInterval int `json:"minimumRegisterIntervalInSeconds"`
}

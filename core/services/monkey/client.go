package monkey

type Client interface {
	Status(jid string) (*StatusResponse, error)
	Update(jid string, body map[string]interface{}) (string, error)
	List() ([]string, error)
}

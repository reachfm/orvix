package stalwart

import "fmt"

type APIClient struct {
	BaseURL string
	Token   string
}

func NewAPIClient(baseURL, token string) *APIClient {
	return &APIClient{BaseURL: baseURL, Token: token}
}

func (c *APIClient) Get(path string) ([]byte, error) {
	return nil, fmt.Errorf("stalwart API client not implemented")
}

func (c *APIClient) Post(path string, body []byte) ([]byte, error) {
	return nil, fmt.Errorf("stalwart API client not implemented")
}

func (c *APIClient) Delete(path string) error {
	return fmt.Errorf("stalwart API client not implemented")
}

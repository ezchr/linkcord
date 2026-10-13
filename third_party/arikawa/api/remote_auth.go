package api

import "github.com/ayn2op/arikawa/v3/utils/httputil"

var EndpointRemoteAuthLogin = EndpointMe + "/remote-auth/login"

// ExchangeRemoteAuthTicket exchanges a remote auth ticket for an authentication token.
// The token must be decrypted using the client's private key.
// Returns the authentication token encrypted with the client's public key.
func (c *Client) ExchangeRemoteAuthTicket(ticket string) (string, error) {
	body := struct {
		Ticket string `json:"ticket"`
	}{ticket}
	resp, err := c.RequestJSON[struct {
		EncryptedToken string `json:"encrypted_token"`
	}]("POST", EndpointRemoteAuthLogin, httputil.WithJSONBody(body))
	return resp.EncryptedToken, err
}

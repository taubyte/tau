package client

func (c *Client) User() *User {
	return &User{client: c}
}

type userDataResponse struct {
	User *UserData `json:"user"`
}

// Get will fetch the user data from the server
// and return UserData and an error
func (u *User) Get() (*UserData, error) {
	if u.userData == nil {
		var response userDataResponse
		err := u.client.Get("/me", &response)
		if err != nil {
			return nil, err
		}

		u.userData = response.User
	}

	return u.userData, nil
}

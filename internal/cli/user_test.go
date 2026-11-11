package cli

import "os/user"

func userCurrent() (string, error) {
	account, err := user.Current()
	if err != nil {
		return "", err
	}
	return account.Username, nil
}

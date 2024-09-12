package messagingLib

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(messaging *structureSpec.Messaging) error {
	return set(messaging, true)
}

func Set(messaging *structureSpec.Messaging) error {
	return set(messaging, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.messaging.Delete()
}

func List() ([]string, error) {
	_, _, channels, err := list()
	if err != nil {
		return nil, err
	}

	return channels, nil
}

func ListResources() ([]*structureSpec.Messaging, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	channels := make([]*structureSpec.Messaging, len(relative))
	for idx, name := range relative {
		messaging, err := project.Messaging(name, application)
		if err != nil {
			return nil, err
		}

		channels[idx], err = messaging.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return channels, nil
}

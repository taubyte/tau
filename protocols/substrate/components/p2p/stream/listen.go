package stream

import "errors"

func (st *Stream) Listen() (protocol string, err error) {
	if st.config == nil {
		return "", errors.New("Stream not instantiated correctly, serviceStruct is nil")
	}

	protocol, err = st.ProtocolHash()
	if err != nil {
		return "", err
	}

	_, err = st.srv.StartStream(st.config.Name, protocol, st.HandleRaw)
	return protocol, err
}

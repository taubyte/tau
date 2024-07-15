package templates

var _templates *templates

func getOrCreateTemplates() *templates {
	if _templates == nil {
		err := loadTemplates()
		if err != nil {
			panic(err)
		}
	}

	return _templates
}

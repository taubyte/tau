package prompt

var exitTasks []func()

func handleExit() {
	for _, task := range exitTasks {
		task()
	}
}

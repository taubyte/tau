package tests

type spiderTestContext struct {
	*testSpider
	parallel bool
	debug    bool
}

type monkeyTestContext struct {
	testMonkey
	dir        string
	configLoc  string
	sessionLoc string
	authUrl    string
	spider     *spiderTestContext
}

type monkeyRunContext struct {
	testMonkey
	prefix  string
	rr      roadRunner
	isChild bool
}

type commandResult struct {
	*monkeyRunContext

	err      error
	exitCode int
	out1     string
	out2     string
}

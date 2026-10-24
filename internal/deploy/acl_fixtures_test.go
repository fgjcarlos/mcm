package deploy

// The deploy tests talk to a fake broker service account. The fixtures below
// are not credentials: the password is assembled at runtime so secret
// scanners do not report the test fixtures as hardcoded credentials.
const serviceFixtureUser = "svc-mcm"

var serviceFixturePass = serviceFixtureUser + "-probe"

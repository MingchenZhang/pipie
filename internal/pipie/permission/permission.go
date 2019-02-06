package permission

// checker API should be stable and not changed
type Checker struct {
	register register
}

// register API is subject to change if new functionalities are needed
type register struct {
	pipeOutAllow bool
	forwardAllow int8
	forwardHost  string
	forwardPort  int
}

func NewRegister() *register {
	return new(register)
}

func (register *register) AllowPortForwardIn(host string, port int) {
	register.forwardAllow = 1
	register.forwardHost = host
	register.forwardPort = port
}

func (register *register) AllowPortForwardOut(host string, port int) {
	register.forwardAllow = 2
	register.forwardHost = host
	register.forwardPort = port
}

func (register *register) AllowPipeOut() {
	register.pipeOutAllow = true
}

func (register *register) BuildChecker() *Checker {
	checker := new(Checker)
	checker.register = *register
	return checker
}

// check permission for forwarding TCP connection.
// receive incoming TCP connection from client, forward it to targetIP:targetPort
func (checker *Checker) CheckPortForwardIn(destHost string, destPort int) bool {
	if checker.register.forwardAllow == 1 {
		if checker.register.forwardHost == "" && checker.register.forwardPort == 0 {
			return true
		}
		if destHost == checker.register.forwardHost && destPort == checker.register.forwardPort {
			return true
		}
	}
	return false
}

// check permission for forwarding TCP connection.
// receive incoming TCP connection from listening socket at targetIP:targetPort, forward it to peer
func (checker *Checker) CheckPortForwardOut(bindHost string, bindPort int) bool {
	if checker.register.forwardAllow == 2 {
		if checker.register.forwardHost == "" && checker.register.forwardPort == 0 {
			return true
		}
		if bindHost == checker.register.forwardHost && bindPort == checker.register.forwardPort {
			return true
		}
	}
	return false
}

// check if pipe outlet is permitted
func (checker *Checker) CheckPipeOut() bool {
	return checker.register.pipeOutAllow
}

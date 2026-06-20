// @title           ChainCoord API
// @version         1.0
// @description     Chain launch coordination server. Coordinators and validators authenticate via secp256k1 challenge-response (ADR-036)
// @host            localhost:8080
// @BasePath        /
// @schemes         http https
//
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Session token obtained from POST /auth/verify. Format: "Bearer <token>"
package main

import "github.com/ny4rl4th0t3p/seedward-chaincoord/cmd/coordd/cmd"

func main() {
	cmd.Execute()
}

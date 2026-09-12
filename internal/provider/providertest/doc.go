// Package providertest ships a scripted fake provider.
//
// Tests build a Provider from a list of Steps, one per expected model call,
// and inspect the requests it received afterwards. It is a fake, not a mock:
// it has no expectations of its own and fails only when a test asks for more
// responses than it scripted.
//
// The entry points are New and the Step constructors Text, Calls, and Fail.
package providertest

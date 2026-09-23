// Package storetest gives a test the PostgreSQL database the store needs.
//
// Call Main from a TestMain: it uses EIKA_TEST_DATABASE_URL when that is set
// and otherwise starts a throwaway postgres:16 container, which it removes
// when the tests are done. Open then hands each test a store on a database of
// its own, migrated and dropped again afterwards, so tests do not see each
// other's rows. Without a database and without Docker every test skips, which
// is why the tests that use this package carry the docker build tag.
package storetest

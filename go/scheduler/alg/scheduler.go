package alg

// Scheduler external interface probably will be accepting
// transactions via a channel in an asynchronous manner.
// Periodically, when the total number of transactions is enough or
// when enough time (max scheduling delay) has passed, the scheduler
// emits a schedule, which is a list of batches of transactions.
type Scheduler interface {
	// AddTransactions add a slice of transactions to the scheduler's queue. As
	// transactions are added, a scheduler will buffer them up until there are enough, and
	// then AddTransaction will run the scheduling algorithm and return an execution
	// schedule.  Some may remain transactions buffered up to be run in the next execution
	// schedule.  If the scheduler is just buffering up transactions and is not ready to
	// return an execution schedule, then a zero-length slice will be returned.
	AddTransactions(t []*Transaction) []*Subgraph

	// FlushSchedule is used to force buffered transactions to be scheduled.  This may
	// occur, for example, as a result of timer expiry.  FlushSchedule is guaranteed to
	// emit a non-zero length slice for execution if there are any transactions buffered,
	// but it is not required to output all buffered transactions, i.e., _some_
	// transactions will be scheduled, but not necessarily _all_.  The caller should
	// repeatedly call FlushSchedule until a zero-length slice is returned, which means
	// that all buffered transactions have been emitted.
	//
	// Deprecation:  have AddTransaction(nil) (empty slice) imply FlushSchedule?
	FlushSchedule() []*Subgraph

	// NumDeferred returns the number of transactions that are queued for scheduling.
	NumDeferred() int
}
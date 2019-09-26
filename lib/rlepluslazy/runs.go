package rlepluslazy

func Add(a, b RunIterator) (RunIterator, error) {

}

type addIt struct {
	a RunIterator
	b RunIterator

	next Run

	arun Run
	brun Run
}

func (it *addIt) prep() error {
	var err error
	if !it.arun.Valid() && it.a.HasNext() {
		it.arun, err = it.a.NextRun()
		if err != nil {
			return err
		}
	}

	if !it.brun.Valid() && it.b.HasNext() {
		it.brun, err = it.b.NextRun()
		if err != nil {
			return err
		}
	}

	if it.arun.Len < it.brun.Len {
		it.next = it.arun
	} else {
		it.next = it.brun
	}
	it.arun.Len = it.arun.Len - it.next.Len
	it.brun.Len = it.brun.Len - it.next.Len

}

func (it *addIt) HasNext() bool {
	return it.next.Valid()
}

func (it *addIt) NextRun() (Run, error) {
	return it.next, it.prep()
}

func Sub(a, b RunIterator) (RunIterator, error) {

}

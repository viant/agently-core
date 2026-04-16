package delete

type Input struct {
	Ids []string `parameter:",kind=body,in=data"`
}

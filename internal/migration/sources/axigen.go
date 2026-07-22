package sources

type AxigenSource struct{}
type ZimbraSource struct{}
type ExchangeSource struct{}
type GenericIMAPSource struct{}

func NewAxigen() *AxigenSource           { return &AxigenSource{} }
func NewZimbra() *ZimbraSource           { return &ZimbraSource{} }
func NewExchange() *ExchangeSource       { return &ExchangeSource{} }
func NewGenericIMAP() *GenericIMAPSource { return &GenericIMAPSource{} }

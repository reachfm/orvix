package cloud

import "fmt"

type S3Adapter struct{}
type GCSAdapter struct{}
type AzureAdapter struct{}

func NewS3Adapter() *S3Adapter       { return &S3Adapter{} }
func NewGCSAdapter() *GCSAdapter     { return &GCSAdapter{} }
func NewAzureAdapter() *AzureAdapter { return &AzureAdapter{} }

func (a *S3Adapter) Upload(path string, data []byte) error    { return fmt.Errorf("not implemented") }
func (a *GCSAdapter) Upload(path string, data []byte) error   { return fmt.Errorf("not implemented") }
func (a *AzureAdapter) Upload(path string, data []byte) error { return fmt.Errorf("not implemented") }

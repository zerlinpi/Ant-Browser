package storage

// ObjectStorage abstracts profile artifact storage.
// Production can use S3/MinIO, development can use local storage.
type ObjectStorage interface {
	Put(path string, data []byte) error
	Get(path string) ([]byte, error)
	Delete(path string) error
}

// LocalStorage is the development implementation.
type LocalStorage struct {
	Root string
}

func NewLocalStorage(root string) *LocalStorage {
	return &LocalStorage{Root: root}
}

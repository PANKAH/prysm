// Package kv defines a persistent backend for the validator service.
package kv

import (
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	prombolt "github.com/prysmaticlabs/prombbolt"
	"github.com/prysmaticlabs/prysm/shared/fileutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	bolt "go.etcd.io/bbolt"
)

// ProtectionDbFileName Validator slashing protection db file name.
var ProtectionDbFileName = "validator.db"

// Store defines an implementation of the Prysm Database interface
// using BoltDB as the underlying persistent kv-store for eth2.
type Store struct {
	db           *bolt.DB
	databasePath string
}

// Close closes the underlying boltdb database.
func (store *Store) Close() error {
	prometheus.Unregister(createBoltCollector(store.db))
	return store.db.Close()
}

func (store *Store) update(fn func(*bolt.Tx) error) error {
	return store.db.Update(fn)
}
func (store *Store) view(fn func(*bolt.Tx) error) error {
	return store.db.View(fn)
}

// ClearDB removes any previously stored data at the configured data directory.
func (store *Store) ClearDB() error {
	if _, err := os.Stat(store.databasePath); os.IsNotExist(err) {
		return nil
	}
	prometheus.Unregister(createBoltCollector(store.db))
	return os.Remove(filepath.Join(store.databasePath, ProtectionDbFileName))
}

// DatabasePath at which this database writes files.
func (store *Store) DatabasePath() string {
	return store.databasePath
}

func createBuckets(tx *bolt.Tx, buckets ...[]byte) error {
	for _, bucket := range buckets {
		if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
			return err
		}
	}
	return nil
}

// NewKVStore initializes a new boltDB key-value store at the directory
// path specified, creates the kv-buckets based on the schema, and stores
// an open connection db object as a property of the Store struct.
func NewKVStore(dirPath string, pubKeys [][48]byte) (*Store, error) {
	hasDir, err := fileutil.HasDir(dirPath)
	if err != nil {
		return nil, err
	}
	if !hasDir {
		if err := fileutil.MkdirAll(dirPath); err != nil {
			return nil, err
		}
	}
	datafile := filepath.Join(dirPath, ProtectionDbFileName)
	boltDB, err := bolt.Open(datafile, params.BeaconIoConfig().ReadWritePermissions, &bolt.Options{Timeout: params.BeaconIoConfig().BoltTimeout})
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, errors.New("cannot obtain database lock, database may be in use by another process")
		}
		return nil, err
	}

	kv := &Store{db: boltDB, databasePath: dirPath}

	if err := kv.db.Update(func(tx *bolt.Tx) error {
		return createBuckets(
			tx,
			genesisInfoBucket,
			historicProposalsBucket,
			historicAttestationsBucket,
			newHistoricAttestationsBucket,
			newHistoricProposalsBucket,
			lowestSignedSourceBucket,
			lowestSignedTargetBucket,
			lowestSignedProposalsBucket,
			highestSignedProposalsBucket,
		)
	}); err != nil {
		return nil, err
	}

	// Initialize the required public keys into the DB to ensure they're not empty.
	if pubKeys != nil {
		if err := kv.UpdatePublicKeysBuckets(pubKeys); err != nil {
			return nil, err
		}
	}

	err = prometheus.Register(createBoltCollector(kv.db))

	return kv, err
}

// UpdatePublicKeysBuckets for a specified list of keys.
func (store *Store) UpdatePublicKeysBuckets(pubKeys [][48]byte) error {
	return store.update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(newHistoricProposalsBucket)
		for _, pubKey := range pubKeys {
			if _, err := bucket.CreateBucketIfNotExists(pubKey[:]); err != nil {
				return errors.Wrap(err, "failed to create proposal history bucket")
			}
		}
		return nil
	})
}

// Size returns the db size in bytes.
func (store *Store) Size() (int64, error) {
	var size int64
	err := store.db.View(func(tx *bolt.Tx) error {
		size = tx.Size()
		return nil
	})
	return size, err
}

// createBoltCollector returns a prometheus collector specifically configured for boltdb.
func createBoltCollector(db *bolt.DB) prometheus.Collector {
	return prombolt.New("boltDB", db)
}

package httpserver

import (
	"errors"
	"fmt"

	"kairo/internal/credentials"
)

// databaseCredentialKey identifies one credential entry without carrying its
// secret.  Keeping this separate from Source makes it possible to stage all
// credential writes before changing a source configuration.
type databaseCredentialKey struct {
	namespace string
	resource  string
	username  string
}

func (k databaseCredentialKey) empty() bool {
	return k.namespace == "" || k.resource == "" || k.username == ""
}

func (k databaseCredentialKey) equal(other databaseCredentialKey) bool {
	return k == other
}

type databaseCredentialSnapshot struct {
	databaseCredentialKey
	value  string
	exists bool
}

// snapshotDatabaseCredentials reads each affected entry before a PUT mutates
// anything.  An unavailable credential backend is fail-closed: callers must
// not update a source while they cannot guarantee rollback.
func snapshotDatabaseCredentials(keys []databaseCredentialKey) ([]databaseCredentialSnapshot, error) {
	seen := make(map[databaseCredentialKey]struct{}, len(keys))
	out := make([]databaseCredentialSnapshot, 0, len(keys))
	for _, key := range keys {
		if key.empty() {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		value, err := credentials.GetResource(key.namespace, key.resource, key.username)
		snapshot := databaseCredentialSnapshot{databaseCredentialKey: key}
		switch {
		case err == nil:
			snapshot.value, snapshot.exists = value, true
		case errors.Is(err, credentials.ErrNotSaved):
			// A missing entry is still part of the snapshot.  Restore removes
			// any newly staged value if a later operation fails.
		case err != nil:
			return nil, fmt.Errorf("读取凭据失败: %w", err)
		}
		out = append(out, snapshot)
	}
	return out, nil
}

// restoreDatabaseCredentials restores every key to the state captured before
// the update.  It intentionally attempts all entries so a partial rollback
// does not leave a new SSH or database password behind.
func restoreDatabaseCredentials(snapshots []databaseCredentialSnapshot) error {
	var errs []error
	for _, snapshot := range snapshots {
		var err error
		if snapshot.exists {
			err = credentials.SaveResource(snapshot.namespace, snapshot.resource, snapshot.username, snapshot.value)
		} else {
			err = credentials.ClearResource(snapshot.namespace, snapshot.resource, snapshot.username)
			if errors.Is(err, credentials.ErrNotSaved) {
				err = nil
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("恢复凭据 %s/%s/%s 失败: %w", snapshot.namespace, snapshot.resource, snapshot.username, err))
		}
	}
	return errors.Join(errs...)
}

func clearDatabaseCredential(key databaseCredentialKey) error {
	if key.empty() {
		return nil
	}
	err := credentials.ClearResource(key.namespace, key.resource, key.username)
	if errors.Is(err, credentials.ErrNotSaved) {
		return nil
	}
	return err
}

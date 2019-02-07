package psql

import (
	//"errors"

	"github.com/NatLibFi/qvain-api/models"
	"github.com/wvh/uuid"
	"log"
	"time"
)

func (db *DB) ChangeOwnerTo(id uuid.UUID, uid uuid.UUID) error {
	tx, err := db.pool.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tag, err := tx.Exec("UPDATE datasets SET owner = $1 WHERE id = $2", uid.Array(), id.Array())
	if err != nil {
		return handleError(err)
	}
	log.Println("tag:", tag)

	return tx.Commit()
}

func (db *DB) Store(dataset *models.Dataset) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.Store(dataset)
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

func (db *DB) BatchStore(datasets []*models.Dataset) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// do something batch-like
	for _, dataset := range datasets {
		err = tx.Store(dataset)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func (tx *Tx) Store(dataset *models.Dataset) error {
	_, err := tx.Exec("INSERT INTO datasets(id, creator, owner, family, schema, blob) VALUES($1, $2, $3, $4, $5, $6)",
		dataset.Id.Array(),
		dataset.Creator.Array(),
		dataset.Owner.Array(),
		dataset.Family(),
		dataset.Schema(),
		dataset.Blob(),
	)
	if err != nil {
		return err
	}

	return nil
}

func (db *DB) Update(id uuid.UUID, blob []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.update(id, blob)
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

// UpdateWithOwner updates a dataset with ownership checks.
func (db *DB) UpdateWithOwner(id uuid.UUID, blob []byte, owner uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return err
	}

	err = tx.update(id, blob)
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

// internal update, user triggered
func (tx *Tx) update(id uuid.UUID, blob []byte) error {
	ct, err := tx.Exec("UPDATE datasets SET modified = now(), seq = seq + 1, blob = $2 WHERE id = $1", id.Array(), blob)
	if err != nil {
		return err
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return nil
}

// internal update, service triggered
func (tx *Tx) updateByService(id uuid.UUID, blob []byte) error {
	ct, err := tx.Exec("UPDATE datasets SET synced = now(), seq = seq + 1, blob = $2 WHERE id = $1", id.Array(), blob)
	if err != nil {
		return err
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return nil
}

func (db *DB) Patch(id uuid.UUID, blob []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.patch(id, blob)
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

// PatchWithOwner patches a dataset JSON blob with ownership checks.
func (db *DB) PatchWithOwner(id uuid.UUID, blob []byte, owner uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return err
	}

	err = tx.patch(id, blob)
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

func (tx *Tx) patch(id uuid.UUID, blob []byte) error {
	ct, err := tx.Exec("UPDATE datasets SET modified = now(), seq = seq + 1, blob = blob || $2 WHERE id = $1", id.Array(), blob)
	if err != nil {
		return err
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return nil
}

func (db *DB) SmartGetWithOwner(id uuid.UUID, owner uuid.UUID) (*models.Dataset, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return nil, err
	}

	famId, err := tx.getFamily(id)
	if err != nil {
		return nil, err
	}

	family, err := models.LookupFamily(famId)
	if err != nil {
		return nil, err
	}

	if family.IsPartial() {
		return tx.get(id, family.Key())
	}
	return tx.get(id, "")
}

func (db *DB) SmartUpdateWithOwner(id uuid.UUID, blob []byte, owner uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return err
	}

	famId, err := tx.getFamily(id)
	if err != nil {
		return err
	}

	family, err := models.LookupFamily(famId)
	if err != nil {
		return err
	}

	if family.IsPartial() {
		err = tx.patch(id, blob)
	} else {
		err = tx.update(id, blob)
	}
	if err != nil {
		return handleError(err)
	}

	return tx.Commit()
}

// StorePublished saves a published dataset to the database and marks it as published.
// TODO: handle empty blob
func (db *DB) StorePublished(id uuid.UUID, blob []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ct, err := tx.Exec("UPDATE datasets SET blob = $2, published = true, synced = now(), seq = seq + 1 WHERE id = $1", id.Array(), blob)
	if err != nil {
		return handleError(err)
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return tx.Commit()
}

func (db *DB) Clone(id uuid.UUID, newid uuid.UUID, blob []byte) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	ct, err := tx.Exec(`
		INSERT INTO datasets(id, creator, owner, created, modified, synced, published, valid, family, schema, blob)
		(SELECT $2, creator, owner, created, modified, synced, published, valid, family, schema, $3 WHERE id = $1)`,
		id, newid, blob)
	if err != nil {
		return handleError(err)
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return tx.Commit()
}

func (tx *Tx) getFamily(id uuid.UUID) (int, error) {
	var fam int
	err := tx.QueryRow("SELECT family FROM datasets WHERE id = $1", id.Array()).Scan(&fam)
	if err != nil {
		return 0, handleError(err)
	}

	return fam, nil
}

// CheckOwner returns an error if the record is not owned by the given user.
func (tx *Tx) CheckOwner(id uuid.UUID, owner uuid.UUID) error {
	var isOwner bool
	err := tx.QueryRow("SELECT (owner = $2) FROM datasets WHERE id = $1", id.Array(), owner.Array()).Scan(&isOwner)
	if err != nil {
		return handleError(err)
	}

	if !isOwner {
		return ErrNotOwner
	}

	return nil
}

// CheckOwner calls tx.CheckOwner to check if the record exists and is owner by the given user.
func (db *DB) CheckOwner(id uuid.UUID, owner uuid.UUID) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	return tx.CheckOwner(id, owner)
}

// publish
//func (db *DB) Publish(id uuid.UUID, )

// MarkPublished marks a dataset as published and updates its sync time. It does not do owner checks.
func (db *DB) MarkPublished(id uuid.UUID, published bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.markPublished(id, published)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// MarkPublishedByOwner marks a dataset as published and updates its sync time. It checks if the given user is the dataset's owner first.
func (db *DB) MarkPublishedWithOwner(id uuid.UUID, owner uuid.UUID, published bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return err
	}

	err = tx.markPublished(id, published)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

// markPublished does the actual marking of a dataset as published.
func (tx *Tx) markPublished(id uuid.UUID, published bool) error {
	ct, err := tx.Exec("UPDATE datasets SET published = $2, synced = $3 WHERE id = $1", id.Array(), published, time.Now())
	if err != nil {
		return err
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return nil
}

func (db *DB) Get(id uuid.UUID) (*models.Dataset, error) {
	var (
		valid  *bool
		family *int
		schema *string
		blob   []byte
	)

	res := new(models.Dataset)
	err := db.pool.QueryRow("select id, creator, owner, valid, family, schema, blob from datasets where id=$1", id.Array()).Scan(res.Id.Array(), res.Creator.Array(), res.Owner.Array(), &valid, &family, &schema, &blob)
	if err != nil {
		return nil, handleError(err)
	}

	err = res.SetData(*family, *schema, blob)
	if err != nil {
		return nil, err
	}

	res.SetValid(*valid)

	return res, nil
}

func (db *DB) GetWithOwner(id uuid.UUID, owner uuid.UUID) (*models.Dataset, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	err = tx.CheckOwner(id, owner)
	if err != nil {
		return nil, err
	}

	return tx.get(id, "")
}

func (tx *Tx) get(id uuid.UUID, key string) (*models.Dataset, error) {
	var (
		family *int
		schema *string
		blob   []byte

		err error
	)

	res := new(models.Dataset)
	if key == "" {
		err = tx.QueryRow("select id, creator, owner, family, schema, blob from datasets where id=$1", id.Array()).Scan(res.Id.Array(), res.Creator.Array(), res.Owner.Array(), &family, &schema, &blob)
	} else {
		err = tx.QueryRow(`select id, creator, owner, family, schema, blob#>$2 from datasets where id=$1`, id.Array(), []string{key}).Scan(res.Id.Array(), res.Creator.Array(), res.Owner.Array(), &family, &schema, &blob)
	}
	if err != nil {
		return nil, handleError(err)
	}

	err = res.SetData(*family, *schema, blob)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (db *DB) Delete(id uuid.UUID, owner *uuid.UUID) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if owner != nil {
		err = tx.CheckOwner(id, *owner)
		if err != nil {
			return handleError(err)
		}
	}

	ct, err := tx.Exec(`DELETE FROM datasets WHERE id = $1`, id.Array())
	if err != nil {
		return handleError(err)
	}

	if ct.RowsAffected() != 1 {
		return ErrNotFound
	}

	return tx.Commit()
}

func (db *DB) ListAllForUid(uid uuid.UUID) ([]*models.Dataset, error) {
	var list []*models.Dataset

	rows, err := db.pool.Query("select id, creator, owner, family, schema, valid from datasets where owner=$1", uid.Array())
	if err != nil {
		return list, err
	}
	defer rows.Close()

	for rows.Next() {
		var dataset models.Dataset
		var (
			family int
			schema string
			valid  bool
		)
		err = rows.Scan(dataset.Id, dataset.Creator, dataset.Owner, family, schema, valid)
		if err != nil {
			return nil, err
		}
		dataset.SetData(family, schema, nil)
		if err != nil {
			return nil, err
		}
		list = append(list, &dataset)
	}

	if rows.Err() != nil {
		return []*models.Dataset{}, rows.Err()
	}

	return list, nil
}

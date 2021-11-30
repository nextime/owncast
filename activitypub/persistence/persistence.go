package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	"github.com/go-fed/activity/streams"
	"github.com/go-fed/activity/streams/vocab"
	"github.com/owncast/owncast/activitypub/apmodels"
	"github.com/owncast/owncast/activitypub/resolvers"
	"github.com/owncast/owncast/core/data"
	"github.com/owncast/owncast/db"
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/utils"

	log "github.com/sirupsen/logrus"
)

var _datastore *data.Datastore

// Setup will initialize the ActivityPub persistence layer with the provided datastore.
func Setup(datastore *data.Datastore) {
	_datastore = datastore
	createFederationFollowersTable()
	createFederationOutboxTable()
	createFederatedActivitiesTable()
}

// AddFollow will save a follow to the datastore.
func AddFollow(follow apmodels.ActivityPubActor, approved bool) error {
	log.Println("Saving", follow.ActorIri, "as a follower.")
	var image string
	if follow.Image != nil {
		image = follow.Image.String()
	}
	return createFollow(follow.ActorIri.String(), follow.Inbox.String(), follow.Name, follow.Username, image, approved)
}

// RemoveFollow will remove a follow from the datastore.
func RemoveFollow(unfollow apmodels.ActivityPubActor) error {
	log.Println("Removing", unfollow.ActorIri, "as a follower.")
	return removeFollow(unfollow.ActorIri)
}

func ApprovePreviousFollowRequest(iri string) error {
	return _datastore.GetQueries().ApproveFederationFollower(context.Background(), db.ApproveFederationFollowerParams{
		Iri: iri,
		ApprovedAt: sql.NullTime{
			Time:  time.Now(),
			Valid: true,
		},
	})
}

func createFollow(actor string, inbox string, name string, username string, image string, approved bool) error {
	// needsApproval := data.GetFollowApprovalRequired()

	_datastore.DbLock.Lock()
	defer _datastore.DbLock.Unlock()

	tx, err := _datastore.DB.Begin()
	if err != nil {
		log.Debugln(err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var approvedAt sql.NullTime
	if approved {
		approvedAt = sql.NullTime{
			Time:  time.Now(),
			Valid: true,
		}
	}

	if err = _datastore.GetQueries().WithTx(tx).AddFollower(context.Background(), db.AddFollowerParams{
		Iri:        actor,
		Inbox:      inbox,
		Name:       sql.NullString{String: name, Valid: true},
		Username:   username,
		Image:      sql.NullString{String: image, Valid: true},
		ApprovedAt: approvedAt,
	}); err != nil {
		log.Errorln("error creating new federation follow", err)
	}

	return tx.Commit()
}

// UpdateFollower will update the details of a stored follower given an IRI.
func UpdateFollower(actorIRI string, inbox string, name string, username string, image string) error {
	_datastore.DbLock.Lock()
	defer _datastore.DbLock.Unlock()

	tx, err := _datastore.DB.Begin()
	if err != nil {
		log.Debugln(err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err = _datastore.GetQueries().WithTx(tx).UpdateFollowerByIRI(context.Background(), db.UpdateFollowerByIRIParams{
		Inbox:    inbox,
		Name:     sql.NullString{String: name, Valid: true},
		Username: username,
		Image:    sql.NullString{String: image, Valid: true},
		Iri:      actorIRI,
	}); err != nil {
		return fmt.Errorf("error updating follower %s %s", actorIRI, err)
	}

	return tx.Commit()
}

func removeFollow(actor *url.URL) error {
	_datastore.DbLock.Lock()
	defer _datastore.DbLock.Unlock()

	tx, err := _datastore.DB.Begin()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	_datastore.GetQueries().WithTx(tx).RemoveFollowerByIRI(context.Background(), actor.String())
	return tx.Commit()
}

// createFederatedActivitiesTable will create the inbound federated
// activities table if needed.
func createFederatedActivitiesTable() {
	createTableSQL := `CREATE TABLE IF NOT EXISTS ap_inbound_activities (
		"id" TEXT NOT NULL,
    "iri" TEXT NOT NULL,
		"account" TEXT,
		"eventType" TEXT,
		"timestamp" DATETIME,
		PRIMARY KEY (id)
	);CREATE INDEX index ON messages (id, account, hidden_at, timestamp);
	CREATE INDEX id ON messages (id);
  CREATE INDEX iri ON messages (iri);
	CREATE INDEX eventType ON messages (eventType);
	CREATE INDEX timestamp ON messages (timestamp);`

	stmt, err := _datastore.DB.Prepare(createTableSQL)
	if err != nil {
		log.Fatal("error creating inbound federated activities table", err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(); err != nil {
		log.Fatal("error creating inbound federated activities table", err)
	}
}

func createFederationOutboxTable() {
	log.Traceln("Creating federation followers table...")
	createTableSQL := `CREATE TABLE IF NOT EXISTS ap_outbox (
		"id" TEXT NOT NULL,
		"iri" TEXT NOT NULL,
		"value" BLOB,
		"type" TEXT NOT NULL,
		"created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (id));
		CREATE INDEX id ON ap_outbox (id);
		CREATE INDEX iri ON ap_outbox (iri);
		CREATE INDEX type ON ap_outbox (type);
	);`

	stmt, err := _datastore.DB.Prepare(createTableSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec()
	if err != nil {
		log.Warnln("error executing sql creating outbox table", createTableSQL, err)
	}
}

func createFederationFollowersTable() {
	log.Traceln("Creating federation followers table...")

	// TODO: Here for resetting testing data. Remove.
	// if _, err := _datastore.DB.Exec("DROP TABLE ap_followers"); err != nil {
	// 	log.Errorln(err)
	// } // TODO: Remove!;

	createTableSQL := `CREATE TABLE IF NOT EXISTS ap_followers (
		"iri" TEXT NOT NULL,
		"inbox" TEXT NOT NULL,
		"name" TEXT,
		"username" TEXT NOT NULL,
		"image" TEXT,
		"approved_at" TIMESTAMP,
		"created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (iri));
		CREATE INDEX iri ON ap_followers (iri);
		CREATE INDEX approved_at ON ap_followers (approved_at);
	);`

	stmt, err := _datastore.DB.Prepare(createTableSQL)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec()
	if err != nil {
		log.Warnln("error executing sql creating followers table", createTableSQL, err)
	}
}

// GetOutbox will return an instance of the outbox populated by stored items.
func GetOutbox() (vocab.ActivityStreamsOrderedCollectionPage, error) {
	collection := streams.NewActivityStreamsOrderedCollectionPage()
	orderedItems := streams.NewActivityStreamsOrderedItemsProperty()
	rows, err := _datastore.GetQueries().GetOutbox(context.Background())
	if err != nil {
		return collection, err
	}

	for _, value := range rows {
		createCallback := func(c context.Context, activity vocab.ActivityStreamsCreate) error {
			orderedItems.AppendActivityStreamsCreate(activity)
			return nil
		}
		if err := resolvers.Resolve(context.Background(), value, createCallback); err != nil {
			return collection, err
		}
	}

	collection.SetActivityStreamsOrderedItems(orderedItems)
	totalCount, _ := _datastore.GetQueries().GetLocalPostCount(context.Background())
	totalItems := streams.NewActivityStreamsTotalItemsProperty()
	totalItems.Set(int(totalCount))
	collection.SetActivityStreamsTotalItems(totalItems)

	return collection, nil
}

// AddToOutbox will store a single payload to the persistence layer.
func AddToOutbox(id string, iri string, itemData []byte, typeString string) error {
	_datastore.DbLock.Lock()
	defer _datastore.DbLock.Unlock()

	tx, err := _datastore.DB.Begin()
	if err != nil {
		log.Debugln(err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err = _datastore.GetQueries().WithTx(tx).AddToOutbox(context.Background(), db.AddToOutboxParams{
		ID:    id,
		Iri:   iri,
		Value: itemData,
		Type:  typeString,
	}); err != nil {
		return fmt.Errorf("error creating new item in federation outbox %s", err)
	}
	// stmt, err := tx.Prepare("INSERT INTO ap_outbox(id, iri, value, type) values(?, ?, ?, ?)")

	// if err != nil {
	// 	log.Debugln(err)
	// }
	// defer stmt.Close()

	// _, err = stmt.Exec(id, iri, itemData, typeString)
	// if err != nil {
	// 	log.Errorln("error creating new item in federation outbox", err)
	// }

	return tx.Commit()
}

// GetObjectByID will return a string representation of a single object by the ID.
func GetObjectByID(id string) (string, error) {
	value, err := _datastore.GetQueries().GetObjectFromOutboxByID(context.Background(), id)
	return string(value), err
}

// GetObjectByIRI will return a string representation of a single object by the IRI.
func GetObjectByIRI(IRI string) (string, error) {
	value, err := _datastore.GetQueries().GetObjectFromOutboxByIRI(context.Background(), IRI)
	return string(value), err
}

// GetLocalPostCount will return the number of posts existing locally.
func GetLocalPostCount() (int64, error) {
	ctx := context.Background()
	return _datastore.GetQueries().GetLocalPostCount(ctx)
}

// GetFollowerCount will return the number of followers we're keeping track of.
func GetFollowerCount() (int64, error) {
	ctx := context.Background()
	return _datastore.GetQueries().GetFollowerCount(ctx)
}

// GetFederationFollowers will return a slice of the followers we keep track of locally.
func GetFederationFollowers(timestamp bool) ([]models.Follower, error) {
	ctx := context.Background()
	followersResult, err := _datastore.GetQueries().GetFederationFollowers(ctx)
	if err != nil {
		return nil, err
	}

	followers := make([]models.Follower, 0)

	for _, row := range followersResult {
		singleFollower := models.Follower{
			Name:     row.Name.String,
			Username: row.Username,
			Image:    row.Image.String,
			Link:     row.Iri,
			Inbox:    row.Inbox,
		}
		if timestamp {
			singleFollower.Timestamp = utils.NullTime(row.CreatedAt)
		}
		followers = append(followers, singleFollower)
	}

	return followers, nil
}

// GetPendingFollowRequests will return pending follow requests.
func GetPendingFollowRequests() ([]models.Follower, error) {
	pendingFollowersResult, err := _datastore.GetQueries().GetFederationFollowerApprovalRequests(context.Background())
	if err != nil {
		return nil, err
	}

	followers := make([]models.Follower, 0)

	for _, row := range pendingFollowersResult {
		singleFollower := models.Follower{
			Name:     row.Name.String,
			Username: row.Username,
			Image:    row.Image.String,
			Link:     row.Iri,
			Inbox:    row.Inbox,
		}
		followers = append(followers, singleFollower)
	}

	return followers, nil
}

// SaveFediverseActivity will save an event to the ap_inbound_activities table.
func SaveFediverseActivity(id string, iri string, accountIRI string, eventType string, timestamp time.Time) error {
	tx, err := data.GetDatastore().DB.Begin()
	if err != nil {
		log.Errorln("error saving", eventType, err)
		return err
	}

	defer tx.Rollback() // nolint

	stmt, err := tx.Prepare("INSERT INTO ap_inbound_activities(id, iri, account, eventType, timestamp) values(?, ?, ?, ?, ?)")
	if err != nil {
		log.Errorln("error saving", eventType, err)
		return err
	}

	defer stmt.Close()

	if _, err = stmt.Exec(id, iri, accountIRI, eventType, timestamp); err != nil {
		log.Errorln("error saving", eventType, err)
		return err
	}
	if err = tx.Commit(); err != nil {
		log.Errorln("error saving", eventType, err)
		return err
	}

	return nil
}

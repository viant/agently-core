package main
import("context";"database/sql";"fmt";_ "github.com/mattn/go-sqlite3";_ "github.com/viant/sqlx/metadata/product/sqlite";"github.com/viant/datly/sql/sequencer")
type row struct { ID string `sqlx:"id,primaryKey"`; Turn string `sqlx:"turn_id"`; Sequence int64 `sqlx:"sequence"` }
func main(){ctx:=context.Background();db,err:=sql.Open("sqlite3",":memory:");if err!=nil{panic(err)};defer db.Close();db.SetMaxOpenConns(1)
 _,err=db.Exec("CREATE TABLE messages(id TEXT PRIMARY KEY,turn_id TEXT,sequence INTEGER,UNIQUE(turn_id,sequence));INSERT INTO messages VALUES('a','t1',2),('b','t2',100)");if err!=nil{panic(err)}
 tx,err:=db.BeginTx(ctx,nil);if err!=nil{panic(err)};defer tx.Rollback();r:=&row{ID:"new",Turn:"t1"};err=sequencer.New(db,tx).Allocate(ctx,"messages",r,"Sequence");fmt.Printf("turn=%s allocated=%d expected_per_turn=3 error=%v\n",r.Turn,r.Sequence,err)
}

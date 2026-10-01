import unittest
from sql_aliases import RESERVED, alias_errors

class MySQLAliasTests(unittest.TestCase):
    def test_primary_keyword_snapshot(self):
        self.assertTrue({'USAGE','ROWS','CONDITION'} <= RESERVED)
        self.assertNotIn('MODEL',RESERVED)
    def test_nested_reserved_alias(self):
        self.assertEqual([(1,'usage')],alias_errors('SELECT * FROM (SELECT * FROM model_call mc) usage JOIN message m ON m.id=usage.id'))
    def test_explicit_alias_and_case(self):
        self.assertEqual([(1,'ROWS')],alias_errors('SELECT * FROM model_call AS ROWS'))
    def test_comma_source_and_projection_commas(self):
        self.assertEqual([(1,'usage')],alias_errors('SELECT a.id, b.cost FROM model_call a, message usage WHERE a.id=usage.id'))
        self.assertEqual([],alias_errors('SELECT a.id, CAST(a.cost AS CHAR) FROM model_call a WHERE a.id IN (1,2)'))
    def test_reserved_cte_name(self):
        self.assertEqual([(1,'usage')],alias_errors('WITH usage AS (SELECT id FROM model_call mc) SELECT * FROM usage u'))
    def test_quoted_alias_preserves_name(self):
        self.assertEqual([],alias_errors('SELECT `usage`.cost FROM (SELECT cost FROM model_call mc) `usage`'))
    def test_literals_comments_and_metadata(self):
        source="""#define($_ = $Query<string>(query/q).WithPredicate(0,'JOIN (SELECT 1) usage'))
SELECT 'FROM x usage', CAST(x.id AS CHAR) FROM model_call x
/* JOIN (SELECT 1) usage */ -- FROM x rows
# MySQL comment FROM x usage
WHERE x.id = 'escaped\\\' JOIN x usage'"""
        self.assertEqual([],alias_errors(source))
    def test_template_sql_branches_still_checked(self):
        self.assertEqual([(1,'usage')],alias_errors('#if($Enabled) SELECT * FROM model_call usage #end'))
    def test_schema_and_parent_join(self):
        self.assertEqual([],alias_errors('SELECT m.id FROM app.message m $View.ParentJoinOn("WHERE","JOIN x usage")'))
    def test_lexical_errors_fail_closed(self):
        with self.assertRaises(ValueError): alias_errors("SELECT * FROM x /* usage")

if __name__=='__main__': unittest.main()

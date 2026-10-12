import unittest
from score_entities import score
class EntityScoreTests(unittest.TestCase):
 def setUp(self):self.gt={'groups':[{'id':'p','kind':'Pod','filter':['worker-.*'],'namespace':'app','root_cause':True},{'id':'s','kind':'Service','filter':['worker'],'namespace':'app'}],'aliases':[['p','s']]}
 def test_alias_and_false_symptom(self):
  got=score(self.gt,{'entities':[{'name':'app/Service/worker','contributing_factor':False},{'name':'app/Pod/worker-123','contributing_factor':True}]});self.assertEqual(got['hit_at_1'],1);self.assertEqual(got['candidate_count'],1)
 def test_wrong_kind_and_namespace_do_not_match(self):
  for name in ['app/Deployment/worker-123','other/Pod/worker-123']:
   self.assertEqual(score(self.gt,{'entities':[{'name':name,'contributing_factor':True}]})['hit_at_1'],0)
 def test_empty_failure_is_zero(self):self.assertEqual(score(self.gt,{'entities':[]})['f1'],0)
if __name__=='__main__':unittest.main()

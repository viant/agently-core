const fs = require('fs');
const assert = require('assert');

const root = '/Users/awitas/go/src/github.vianttech.com/viant/steward_ai/deployment/steward';
const template = fs.readFileSync(`${root}/templates/recommendation_review_dashboard.yaml`, 'utf8');

assert.match(
  template,
  /recommendation review is a staged lifecycle[\s\S]*review[\s\S]*validate[\s\S]*execute[\s\S]*follow_up/,
  'recommendation review template must declare the staged lifecycle'
);

assert.match(
  template,
  /Include one JSON datasource with id `workflow_stage`\./,
  'recommendation review template must require the workflow_stage datasource'
);

assert.match(
  template,
  /callback\.context\.stageLifecycle[\s\S]*currentStage[\s\S]*validationStage[\s\S]*successStage[\s\S]*followUpStage/,
  'recommendation review template must require stageLifecycle callback context'
);

console.log('recommendation stage lifecycle contract verified');

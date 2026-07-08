import { describe, expectTypeOf, it } from 'vitest';

import type { OOBLoginInput } from '../types';

describe('OOBLoginInput', () => {
  it('matches the OOB auth contract used by the backend and mobile SDKs', () => {
    expectTypeOf<OOBLoginInput>().toMatchTypeOf<{
      secretsURL?: string;
      scopes?: string[];
      accessToken?: string;
      idToken?: string;
      refreshToken?: string;
      username?: string;
    }>();
  });
});

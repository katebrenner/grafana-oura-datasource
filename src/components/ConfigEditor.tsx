import React, { ChangeEvent, useEffect, useRef, useState } from 'react';
import { InlineField, Input, SecretInput, Button, Alert } from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { MyDataSourceOptions, MySecureJsonData } from '../types';

const OURA_AUTHORIZE_URL = 'https://cloud.ouraring.com/oauth/authorize';
const OURA_SCOPE = 'personal daily email';
const AUTH_CODE_PATTERN = /code=([\w-]+)/;
const EXCHANGE_TIMEOUT_MS = 20000;

interface Props extends DataSourcePluginOptionsEditorProps<MyDataSourceOptions, MySecureJsonData> {}

export function ConfigEditor(props: Props) {
  const { onOptionsChange, options } = props;
  const { jsonData, secureJsonFields, secureJsonData } = options;
  const [oauthError, setOauthError] = useState<string | null>(null);
  const [exchangePending, setExchangePending] = useState(false);
  const [exchangeSuccess, setExchangeSuccess] = useState(false);
  const exchangeStartedRef = useRef(false);

  const currentLocation = typeof window !== 'undefined' ? window.location.origin + window.location.pathname : '';
  const hasCodeInUrl = typeof window !== 'undefined' && AUTH_CODE_PATTERN.test(window.location.search);
  const hasErrorInUrl = typeof window !== 'undefined' && /error=/.test(window.location.search);

  // Strava-style: when config page loads with ?code= after Oura redirect, exchange code for tokens (once per load)
  useEffect(() => {
    if (!hasCodeInUrl || !options.uid) {
      return;
    }
    const match = window.location.search.match(AUTH_CODE_PATTERN);
    const code = match?.[1];
    if (!code) {
      return;
    }
    if (exchangeStartedRef.current) {
      return;
    }
    exchangeStartedRef.current = true;

    queueMicrotask(() => {
      setExchangePending(true);
      setOauthError(null);
    });
    const redirectUri = currentLocation;
    const url = `/api/datasources/uid/${options.uid}/resources/oauth/exchange`;

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), EXCHANGE_TIMEOUT_MS);

    const exchangePromise = fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code, redirect_uri: redirectUri }),
      credentials: 'include',
      signal: controller.signal,
    }).then(async (res) => {
      clearTimeout(timeoutId);
      const text = await res.text();
      if (!res.ok) {
        throw new Error(text || res.statusText || `Token exchange failed (${res.status})`);
      }
      try {
        return JSON.parse(text) as { access_token: string; refresh_token: string };
      } catch {
        throw new Error('Invalid response from server');
      }
    });

    exchangePromise
      .then((data) => {
        onOptionsChange({
          ...options,
          secureJsonFields: {
            ...options.secureJsonFields,
            accessToken: true,
            refreshToken: !!data.refresh_token,
          },
          secureJsonData: {
            ...options.secureJsonData,
            accessToken: data.access_token,
            refreshToken: data.refresh_token ?? '',
          },
        });
        window.history.replaceState(null, '', window.location.pathname);
        setExchangeSuccess(true);
      })
      .catch((err: unknown) => {
        const message =
          err instanceof Error
            ? err.name === 'AbortError'
              ? 'Token exchange timed out. Check that the redirect URI in Oura matches this page.'
              : err.message
            : 'Token exchange failed';
        setOauthError(message);
      })
      .finally(() => {
        setExchangePending(false);
      });
  }, [hasCodeInUrl, options.uid, currentLocation, options, onOptionsChange]);

  const onClientIdChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        clientId: event.target.value,
      },
    });
  };

  const onClientSecretChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: {
        ...secureJsonData,
        clientSecret: event.target.value,
      },
    });
  };

  const onResetClientSecret = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...options.secureJsonFields,
        clientSecret: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        clientSecret: '',
      },
    });
  };

  const onClearOAuthTokens = () => {
    setOauthError(null);
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...options.secureJsonFields,
        accessToken: false,
        refreshToken: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        accessToken: '',
        refreshToken: '',
      },
    });
  };

  // Strava-style: direct link to Oura authorize URL with redirect_uri = config page
  const connectWithOuraHref =
    jsonData.clientId?.trim() && currentLocation
      ? `${OURA_AUTHORIZE_URL}?response_type=code&client_id=${encodeURIComponent(jsonData.clientId)}&redirect_uri=${encodeURIComponent(currentLocation)}&scope=${encodeURIComponent(OURA_SCOPE)}`
      : '#';
  const showConnectButton = !!jsonData.clientId?.trim() && secureJsonFields?.clientSecret === true;
  const hasOAuthToken = secureJsonFields?.accessToken === true;

  return (
    <>
      <InlineField label="Client ID" labelWidth={14} interactive tooltip={'OAuth client ID from Oura app settings'}>
        <Input
          id="config-editor-client-id"
          onChange={onClientIdChange}
          value={jsonData.clientId ?? ''}
          placeholder="Enter your client ID"
          width={40}
        />
      </InlineField>
      <InlineField label="Client Secret" labelWidth={14} interactive tooltip={'OAuth client secret (backend only)'}>
        <SecretInput
          id="config-editor-client-secret"
          isConfigured={secureJsonFields?.clientSecret}
          value={secureJsonData?.clientSecret}
          placeholder="Enter your client secret"
          width={40}
          onReset={onResetClientSecret}
          onChange={onClientSecretChange}
        />
      </InlineField>
      <InlineField
        label="Oura OAuth"
        labelWidth={14}
        interactive
        tooltip={'Authorize with Oura; redirect URI is this config page. Add this URL to Oura app redirect URIs.'}
      >
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          {showConnectButton && (
            <Button
              type="button"
              variant="primary"
              disabled={exchangePending}
              onClick={() => {
                window.location.href = connectWithOuraHref;
              }}
            >
              {hasOAuthToken ? 'Reconnect with Oura' : 'Connect with Oura'}
            </Button>
          )}
          {hasOAuthToken && (
            <Button type="button" variant="secondary" onClick={onClearOAuthTokens}>
              Clear OAuth token
            </Button>
          )}
        </div>
      </InlineField>
      {hasCodeInUrl && exchangePending && (
        <InlineField label="" labelWidth={14}>
          <Alert severity="info" title="Exchanging authorization code…" />
        </InlineField>
      )}
      {exchangeSuccess && (
        <InlineField label="" labelWidth={14}>
          <Alert
            severity="success"
            title="Auth code successfully obtained. Save data source to finish authentication."
          />
        </InlineField>
      )}
      {hasErrorInUrl && (
        <InlineField label="" labelWidth={14}>
          <Alert severity="error" title="Error obtaining auth code." />
        </InlineField>
      )}
      {oauthError && (
        <InlineField label="" labelWidth={14}>
          <Alert severity="error" title={oauthError} />
        </InlineField>
      )}
    </>
  );
}

/** Presentation for the native forms after IP whitelist in the guided picker.
 * The backend schema still owns the field inventory; placeholders never set values.
 */
export interface MiddlewareFieldGroup {
 id: string;
 labelKey?: string;
 descriptionKey?: string;
 fields: string[];
 collapsible?: boolean;
 plain?: boolean;
}
const main = (id: string, fields: string[]): MiddlewareFieldGroup => ({ id, fields, plain: true });
const options = (id: string, fields: string[], labelKey = 'moreOptions', descriptionKey?: string): MiddlewareFieldGroup =>
 ({ id, fields, labelKey, descriptionKey, collapsible: true });

export const remainingTypeGroups: Record<string, MiddlewareFieldGroup[]> = {
 headers: [
  main('requestResponse', ['customRequestHeaders', 'customResponseHeaders']),
  options('cors', ['accessControlAllowOriginList', 'accessControlAllowOriginListRegex', 'accessControlAllowMethods', 'accessControlAllowHeaders', 'accessControlExposeHeaders', 'accessControlAllowCredentials', 'accessControlMaxAge', 'addVaryHeader'], 'headersCors', 'headersCorsDesc'),
  options('security', ['frameDeny', 'customFrameOptionsValue', 'contentTypeNosniff', 'contentSecurityPolicy', 'contentSecurityPolicyReportOnly', 'referrerPolicy', 'permissionsPolicy', 'allowedHosts', 'hostsProxyHeaders', 'browserXssFilter', 'customBrowserXSSValue', 'featurePolicy', 'publicKey', 'isDevelopment'], 'headersSecurity', 'headersSecurityDesc'),
  options('ssl', ['stsSeconds', 'stsIncludeSubdomains', 'stsPreload', 'forceSTSHeader', 'sslRedirect', 'sslTemporaryRedirect', 'sslForceHost', 'sslHost', 'sslProxyHeaders'], 'headersSsl', 'headersSslDesc')
 ],
 passTLSClientCert: [main('certificate', ['pem']), options('certificateInfo', ['info'], 'certificateInfo', 'certificateInfoDesc')],
 addPrefix: [main('path', ['prefix'])],
 stripPrefix: [main('prefixes', ['prefixes']), options('options', ['forceSlash'])],
 stripPrefixRegex: [main('patterns', ['regex'])],
 replacePath: [main('path', ['path'])],
 replacePathRegex: [main('rewrite', ['regex', 'replacement'])],
 redirectRegex: [main('redirect', ['regex', 'replacement']), options('options', ['permanent'])],
 redirectScheme: [main('scheme', ['scheme']), options('options', ['port', 'permanent'])],
 compress: [main('encodings', ['encodings']), options('media', ['includedContentTypes', 'excludedContentTypes'], 'compressionMedia', 'compressionMediaDesc'), options('options', ['minResponseBodyBytes', 'defaultEncoding'])],
 contentType: [main('detection', ['autoDetect'])],
 grpcWeb: [main('origins', ['allowOrigins'])],
 encodedCharacters: [main('separators', ['allowEncodedSlash', 'allowEncodedBackSlash']), options('characters', ['allowEncodedPercent', 'allowEncodedQuestionMark', 'allowEncodedHash', 'allowEncodedSemicolon', 'allowEncodedNullCharacter'], 'encodedOptions', 'encodedOptionsDesc')],
 errors: [main('errorPage', ['status', 'service']), options('options', ['query', 'statusRewrites', 'errorRequestHeaders'])]
};

export const remainingRequiredFields: Record<string, readonly string[]> = {
 headers: [], passTLSClientCert: [], addPrefix: ['prefix'], stripPrefix: ['prefixes'], stripPrefixRegex: ['regex'],
 replacePath: ['path'], replacePathRegex: ['regex', 'replacement'], redirectRegex: ['regex', 'replacement'],
 redirectScheme: ['scheme'], compress: [], contentType: [], grpcWeb: [], encodedCharacters: [], errors: ['status', 'service']
};

/** Examples, not implied runtime defaults. API identifiers remain untranslated. */
const examples: Record<string, string> = {
 prefix: '/api', path: '/health', prefixes: '/api', regex: '^/api/(.*)', replacement: '/${1}',
 scheme: 'https', port: '443', expression: 'NetworkErrorRatio() > 0.5', retryExpression: 'IsNetworkError() && Attempts() < 2',
 address: 'https://auth.example.com/verify', authSigninURL: 'https://auth.example.com/login',
 period: '1s', checkPeriod: '10s', fallbackDuration: '10s', recoveryDuration: '10s', initialInterval: '100ms', timeout: '5s',
 dialTimeout: '5s', readTimeout: '3s', writeTimeout: '3s', attempts: '3', average: '100', burst: '50', amount: '20',
 responseCode: '503', rejectStatusCode: '403', sourceRange: '192.0.2.0/24', excludedIPs: '192.0.2.10/32', depth: '1', ipv6Subnet: '64',
 encodings: 'gzip', defaultEncoding: 'gzip', minResponseBodyBytes: '1024', includedContentTypes: 'application/json', excludedContentTypes: 'text/event-stream',
 allowOrigins: 'https://app.example.com', status: '500-599', statusRewrites: '500', query: '/errors/{status}.html',
 service: 'error-pages@file', errorRequestHeaders: 'X-Request-ID', middlewares: 'security-headers@file',
 headerField: 'X-Authenticated-User', requestHeaderName: 'X-Forwarded-For', realm: 'Restricted area',
 endpoints: 'redis.example.internal:6379', db: '0', poolSize: '10', maxActiveConns: '20', minIdleConns: '2',
 maxRequestBodyBytes: '10485760', maxResponseBodyBytes: '10485760', memRequestBodyBytes: '1048576', memResponseBodyBytes: '1048576',
 maxBodySize: '1048576', maxResponseBodySize: '1048576',
 accessControlAllowHeaders: 'Content-Type', accessControlAllowMethods: 'GET', accessControlAllowOriginList: 'https://app.example.com',
 accessControlAllowOriginListRegex: '^https://([a-z0-9-]+\\.)?example\\.com$', accessControlExposeHeaders: 'X-Request-ID',
 accessControlMaxAge: '600', allowedHosts: 'app.example.com', hostsProxyHeaders: 'X-Forwarded-Host',
 customBrowserXSSValue: '1; mode=block', customFrameOptionsValue: 'SAMEORIGIN',
 contentSecurityPolicy: "default-src 'self'", contentSecurityPolicyReportOnly: "default-src 'self'; report-uri /csp-report",
 permissionsPolicy: 'camera=(), microphone=()', featurePolicy: "camera 'none'", referrerPolicy: 'strict-origin-when-cross-origin',
 publicKey: 'pin-sha256="base64-digest"; max-age=2592000', sslHost: 'app.example.com', stsSeconds: '31536000',
 authRequestHeaders: 'Authorization', authResponseHeaders: 'X-Authenticated-User', authResponseHeadersRegex: '^X-Auth-', addAuthCookiesToResponse: 'session',
 username: 'service-account', password: '••••••••'
};
export function fieldExample(type: string, path: string): string | undefined {
 const key = path.split('.').at(-1) ?? path;
 if (type === 'redirectRegex' && key === 'regex') return '^http://([^/]+)/(.*)';
 if (type === 'redirectRegex' && key === 'replacement') return 'https://${1}/${2}';
 return examples[path] ?? examples[key];
}

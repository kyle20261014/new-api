export function diagnosticStateKey(state: string): string {
  const keys: Record<string, string> = {
    queued: 'Queued',
    running: 'Running',
    completed: 'Completed',
    partial: 'Partially completed',
    failed: 'Failed',
    unauthorized: 'Permission expired',
    review_pending: 'Review pending',
    settled: 'Settled',
    pending: 'Pending',
    passed: 'Passed',
    incomplete: 'Incomplete',
    unknown: 'Unable to determine',
    structural_anomaly: 'Structural anomaly',
    structure_passed: 'Structure passed',
    structure_failed: 'Structure failed',
    not_applicable: 'Not applicable',
  }
  return keys[state] ?? state
}

export function isolatedPreviewDocument(source: string): string {
  // Preserve html/body attributes and never parse the document in the application's DOM.
  // Sandbox blocks top navigation, but CSP cannot guarantee blocking iframe self-navigation.
  return `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; navigate-to 'none'"><meta name="referrer" content="no-referrer">${source}`
}

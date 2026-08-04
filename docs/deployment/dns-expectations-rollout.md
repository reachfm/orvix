# Rolling out the canonical DNS expectations

The admin **Domains → DNS** console derives every "Required value" it shows —
and every value it verifies against, explains in repair guidance, and writes
into the downloadable record file — from server configuration under
`coremail:` in `orvix.yaml`.

These keys are **optional and default to empty**. That is deliberate and is
the supported upgrade path:

> When an expectation is not configured, ORVIX reports that record as
> `configuration_required`. It keeps displaying the record the domain
> actually publishes, names the missing setting, and does **not** invent a
> requirement. An unconfigured record can never be reported as passing and
> can never allow a 100% result.

This matters because an installation upgraded from a YAML that predates these
keys is usually already publishing a **correct** policy. Inventing a generic
default (for example `v=spf1 mx -all`) would make the console contradict a
valid record and instruct the operator to replace it.

## Keys

| Key | Meaning | Unset behaviour |
| --- | --- | --- |
| `coremail.expected_spf` | Literal SPF TXT value tenants must publish | SPF row → `configuration_required`; no SPF matching performed |
| `coremail.expected_dmarc_policy` | DMARC `p=` value (`none`/`quarantine`/`reject`) | Defaults to `quarantine` **once** `expected_dmarc_rua` is set |
| `coremail.expected_dmarc_rua` | Aggregate-report destination | DMARC row → `configuration_required`; no address is fabricated |
| `coremail.autodiscover_srv_target` | Host terminating autodiscover | SRV row → `configuration_required`; target is never guessed |
| `coremail.autodiscover_srv_port` | SRV port | Defaults to `443` once a target is set |
| `coremail.autodiscover_srv_priority` | SRV priority | `0` |
| `coremail.autodiscover_srv_weight` | SRV weight | `0` |

Notes on the two "never invented" cases:

- **DMARC rua.** ORVIX does not provision a `dmarc@<domain>` mailbox. Showing
  one as the required value would send an operator to publish a reporting
  address that silently discards their aggregate reports.
- **Autodiscover SRV target.** Autodiscover may be terminated by a host that
  is not an MX host, so deriving the target from the mail host would report
  "wrong target" against a record that is in fact correct.

## Rollout procedure

1. Determine the deployment's **real** published policies (`dig TXT <domain>`,
   `dig TXT _dmarc.<domain>`, `dig SRV _autodiscover._tcp.<domain>`). Do not
   guess them and do not change public DNS as part of this rollout — the goal
   is to teach ORVIX what is already true.
2. Add the keys to `/etc/orvix/orvix.yaml` under the existing `coremail:`
   block, alongside `hostname` and `expected_mx`.
3. Restart the service once and reload the admin Domains → DNS console.
4. Confirm each row moves from `configuration_required` to a real verdict.
   A row that now reads `warning`/`fail` is a genuine finding about published
   DNS, not a configuration gap — fix DNS separately and deliberately.

Roll back by removing the keys: records return to `configuration_required`
and no verdict is asserted.

## Worked example (placeholders)

```yaml
coremail:
  hostname: "mail.example.com"
  expected_mx:
    - "mx1.example.com"
    - "mx2.example.com"

  expected_spf: "v=spf1 ip4:198.51.100.10 include:spf.example.com -all"
  expected_dmarc_policy: "quarantine"
  expected_dmarc_rua: "mailto:dmarc-reports@example.com"

  autodiscover_srv_target: "autodiscover.example.com"
  autodiscover_srv_port: 443
  autodiscover_srv_priority: 0
  autodiscover_srv_weight: 0
```

Every value above is a **placeholder**. Repository defaults intentionally
carry no real host, IP, or domain; substitute the values for the deployment
being configured. The concrete values for a given environment belong in that
host's `/etc/orvix/orvix.yaml`, not in version control.

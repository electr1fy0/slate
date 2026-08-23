import json
import os
import time

import boto3
from boto3.dynamodb.conditions import Attr

TABLE_NAME = os.environ["TABLE_NAME"]
LOOKBACK_MINUTES = int(os.environ.get("LOOKBACK_MINUTES", "30"))
METRIC_NAMESPACE = os.environ.get("METRIC_NAMESPACE", "Continuum")

dynamodb = boto3.resource("dynamodb").Table(TABLE_NAME)
cloudwatch = boto3.client("cloudwatch")


def handler(event, context):
    cutoff_ms = int((time.time() - LOOKBACK_MINUTES * 60) * 1000)
    journal = []
    kwargs = {"FilterExpression": Attr("pk").begins_with("JOURNAL#") & Attr("created_at").gte(cutoff_ms)}
    while True:
        resp = dynamodb.scan(**kwargs)
        journal.extend(resp.get("Items", []))
        if "LastEvaluatedKey" not in resp:
            break
        kwargs["ExclusiveStartKey"] = resp["LastEvaluatedKey"]

    repaired, already_ok, conflicts = [], [], []

    for entry in journal:
        target_key = {"pk": entry["target_pk"], "sk": entry["target_sk"]}
        current = dynamodb.get_item(Key=target_key).get("Item")

        if current and current.get("request_id") == entry["request_id"]:
            already_ok.append(entry["request_id"])
            continue
        if current and current.get("version", 0) >= entry["version"]:
            conflicts.append({"request_id": entry["request_id"],
                              "reason": "newer version present",
                              "journal_version": int(entry["version"]),
                              "current_version": int(current.get("version", 0))})
            continue

        try:
            dynamodb.put_item(
                Item={**target_key,
                      "answers": entry.get("answers", {}),
                      "version": entry["version"],
                      "request_id": entry["request_id"],
                      "updated_at": entry["created_at"],
                      "reconciled": True,
                      "written_by_region": os.environ.get("AWS_REGION", "")},
                ConditionExpression="attribute_not_exists(pk) OR version < :v",
                ExpressionAttributeValues={":v": entry["version"]})
            repaired.append(entry["request_id"])
        except dynamodb.meta.client.exceptions.ConditionalCheckFailedException:
            conflicts.append({"request_id": entry["request_id"], "reason": "lost race"})

    summary = {"scanned": len(journal), "repaired": len(repaired),
               "already_replicated": len(already_ok), "conflicts": conflicts,
               "repaired_ids": repaired}
    cloudwatch.put_metric_data(
        Namespace=METRIC_NAMESPACE,
        MetricData=[
            {"MetricName": "ReconcilerRepaired", "Value": len(repaired)},
            {"MetricName": "ReconcilerScanned", "Value": len(journal)},
        ])
    print(json.dumps(summary))
    return summary

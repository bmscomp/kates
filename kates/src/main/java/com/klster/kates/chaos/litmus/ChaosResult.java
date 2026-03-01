package com.klster.kates.chaos.litmus;

import io.fabric8.kubernetes.api.model.Namespaced;
import io.fabric8.kubernetes.client.CustomResource;
import io.fabric8.kubernetes.model.annotation.Group;
import io.fabric8.kubernetes.model.annotation.Version;

@Group("litmuschaos.io")
@Version("v1alpha1")
public class ChaosResult extends CustomResource<Void, ChaosResultStatus> implements Namespaced {}

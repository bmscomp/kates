package com.bmscomp.kates.chaos.litmus;

import io.fabric8.kubernetes.api.model.Namespaced;
import io.fabric8.kubernetes.client.CustomResource;
import io.fabric8.kubernetes.model.annotation.Group;
import io.fabric8.kubernetes.model.annotation.Version;

@Group("litmuschaos.io")
@Version("v1alpha1")
public class ChaosExperiment extends CustomResource<Void, Void> implements Namespaced {

    // We only need the metadata to read labels/annotations for dynamic discovery,
    // which CustomResource already provides via getMetadata().

}
